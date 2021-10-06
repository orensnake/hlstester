package main

import (
	"fmt"
	"github.com/orensnake/i18n"
	"io/ioutil"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
	"unsafe"
)

const (
	MSG_READER_STAT       = 7
	MSG_STAT_REQ          = 8
	MSG_STAT_ERR          = 9
	MSG_STAT_BYTES        = 10
	MSG_STAT_SPEED        = 11
	MSG_PL                = 12
	MSG_PL_CHUNK          = 13
	MSG_YES               = 14
	MSG_NO_LOADED         = 15
	MSG_NO                = 16
	MSG_STATUS_ERR        = 17
	MSG_WORKER_TRY_TO_GET = 18
	MSG_LOADED_CHUNK_INFO = 19
	MSG_PL_RELOAD         = 20
)

type Reader struct {
	url  string   // URL плейлиста
	ID   int      // Идентификатор "потока"
	St   stat     // Статистика "потока"
	Pl   playlist // Плейлист
	stop bool     // Разршение на остановку периодических действий
}

type stat struct {
	connections   int           // Число выполненных запросов к стриммеру
	bytesReceived int64         // Получено байт от сервера
	errors        int           // Количество ошибок при приеме
	sec           time.Duration // Общее время, потраченное на получение bytesReceived
}

type playlist struct {
	url           string // url для получения плейлиста
	st            *stat  // Статистика
	ID            int
	Chunks        []chunkElem // Описание чанков - имя / получен / заблокирован (отправлен запрос на получение)
	lastChunkTime float64     // Длительность последнего чанка в секундах
}

type chunkElem struct { // Описатель чанков
	Chunk  string // имя чанка
	Loaded bool   // Загружен
	Locked bool   // заблокирован (отправлен запрос на получение)
}

type MyErrorCode struct{}

func (m *MyErrorCode) Error() string {
	return i18n.Translation.GetText(MSG_STATUS_ERR)
}

func (pl *playlist) Init(url string, st *stat, id int) {
	pl.url = url
	pl.st = st
	pl.lastChunkTime = 2.0
	pl.ID = id
}

func (pl *playlist) GetPlaylist() error {
	// Выполняем http/https запрос к плейлисту
	fmt.Printf(i18n.Translation.GetText(MSG_PL_RELOAD), pl.ID)
	resp, err := http.Get(pl.url)
	if err != nil {
		// В случае ошибок - увеличиваем счетчик ошибок и выходим
		pl.st.errors += 1
		fmt.Println("GetPlaylist/Get error:", err)
		return err
	}
	defer resp.Body.Close()

	// Получаем ответ
	answer, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		// В случае ошибок - увеличиваем счетчик ошибок и выходим
		fmt.Println("GetPlaylist/ReadAll error:", err)
		pl.st.errors += 1
		return err
	}

	if resp.StatusCode != 200 {
		err = &MyErrorCode{}
		fmt.Printf("GetPlaylist/StatusCode error: %v. Answer is \"%v\"\n", err, string(answer))
		pl.st.errors += 1
		return err
	}
	// Парсим ответ
	strArr := strings.Split(string(answer), "\n") // []byte => []string

	lastChunkTimeStr := "5.0"
	for i := 0; i < len(strArr); i++ {
		if !strings.HasPrefix(strArr[i], "#") && strArr[i] != "" {
			// Это чанк - strArr[i]
			found := false
			// Проход по всем элементам нашего "хранилища чанков"
			for j := 0; j < len(pl.Chunks); j++ {
				// Если чанка нет в списке, то пометим как неполученный
				if pl.Chunks[j].Chunk == strArr[i] {
					found = true
				}
			}
			if !found {
				// Если нет такого чанка в нашем "хранилище чанков" - добавляем
				pl.Chunks = append(pl.Chunks, chunkElem{strArr[i], false, false})
			}
		} else {
			// Начинается на #
			if strings.HasPrefix(strArr[i], "#EXTINF:") {
				// Выделим длительность последнего чанка
				lastChunkTimeStr = strings.TrimSuffix(strings.TrimPrefix(strArr[i], "#EXTINF:"), ",")
			}
		}
	}
	// Обновим длительность последнего чанка
	pl.lastChunkTime, err = strconv.ParseFloat(lastChunkTimeStr, 64)
	// Устанавливаем частоту опроса плейлиста как половину от длительности последнего чанка - небольшой запас в 2%
	pl.lastChunkTime = pl.lastChunkTime * 0.48

	// Удалим старые чанки (полученные и отсутствующие в новом плейлисте)
	for i := 0; i < len(pl.Chunks); i++ {
		found := false
		for j := 0; j < len(strArr); j++ {
			if pl.Chunks[i].Chunk == strArr[j] {
				found = true
			}
		}
		if !found {
			// удаляем элемент
			pl.Chunks = append(pl.Chunks[:i], pl.Chunks[i+1:]...)
		}
	}

	// Обновляем статистику
	pl.st.bytesReceived += resp.ContentLength + int64(unsafe.Sizeof(resp.Header)) // Увеличим размер полученных данных
	pl.st.connections += 1                                                        // Увеличим число обращений к серверу

	return nil
}

func (r *Reader) Init(url string, id int) {
	// Сохраняем необходимые поля
	r.url = url
	r.ID = id
	r.stop = false
	r.Pl.Init(url, &r.St, r.ID)
}

func (r *Reader) Stop() {
	r.stop = true
}

func (r *Reader) updatePlaylist() {
	for !r.stop {
		err := r.Pl.GetPlaylist()
		if err == nil {
			// При отсутствиии ошибки - делаем паузу перед следующей попыткой получения плейлиста
			// При ошибке - тут же повторяем попытку получения!
			p, _ := time.ParseDuration(fmt.Sprintf("%vs", r.Pl.lastChunkTime)) // нет смысла запрашивать обновления плейлиста ранее
			time.Sleep(p)
		}
	}
}

func (r *Reader) clearChunkLock(chunkName string) {
	// Ищем соответствующий chunk
	for i := 0; i < len(r.Pl.Chunks); i++ {
		if r.Pl.Chunks[i].Chunk == chunkName {
			// Снимаем с него блокировку
			r.Pl.Chunks[i].Locked = false
		}
	}
}

func (r *Reader) getChunk(chunkName string) {
	t1 := time.Now() // Текущее время для вычисления длительности загрузки

	// Строим пусть к чанку исходя из пути к плейлисту
	u, _ := url.Parse(r.Pl.url)
	u.RawQuery = "" // Убираем параметры доступа для плейлиста - токены/SL

	if strings.HasPrefix(chunkName, "/") {
		// Абсолютный путь к чанку
		u.Path = chunkName
	} else {
		// относительный путь
		u.Path = path.Join(path.Dir(u.Path), chunkName)
	}

	fmt.Printf(i18n.Translation.GetText(MSG_WORKER_TRY_TO_GET), r.ID, u.String())
	// Пытаемся получить чанк
	resp, err := http.Get(u.String())
	if err != nil {
		fmt.Println("getChunk/get error:", err)
		r.Pl.st.errors += 1
		r.clearChunkLock(chunkName) // При ошибке снимаем блокировку с чанка
	} else {
		defer resp.Body.Close()
		// Получаем ответ
		_, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			fmt.Println("getChunk/ReadAll error:", err)
			r.Pl.st.errors += 1
			r.clearChunkLock(chunkName) // При ошибке снимаем блокировку с чанка
		} else {
			t2 := time.Now().Sub(t1) // Данные получены, считаем длительность выполнения
			dataSize := resp.ContentLength + int64(unsafe.Sizeof(resp.Header))
			speed := float64(dataSize) * 8.0 / t2.Seconds() / (1024 * 1024)
			r.Pl.st.bytesReceived += dataSize // Увеличиваем объем полученных данных в статистике
			fmt.Printf(i18n.Translation.GetText(MSG_LOADED_CHUNK_INFO), r.ID, u.String(), dataSize, t2, fmt.Sprintf("%.2f", speed))

			r.Pl.st.sec += t2        // Увеличиваем общий счетчик времени
			r.Pl.st.connections += 1 // и число запросов

			// ищем данный чанк в листе и помечаем скачнным
			for i := 0; i < len(r.Pl.Chunks); i++ {
				if r.Pl.Chunks[i].Chunk == chunkName {
					r.Pl.Chunks[i].Loaded = true
				}
			}
		}
	}
}

func (r *Reader) readChunks() {
	for !r.stop { // Пока разрешено работать ридеру
		for i := 0; i < len(r.Pl.Chunks); i++ {
			// Ищем в "хранилище чанков" не скачанные и незаблокированные элемента
			if !r.Pl.Chunks[i].Loaded && !r.Pl.Chunks[i].Locked {
				r.Pl.Chunks[i].Locked = true        // Помечаем заблокированным
				go r.getChunk(r.Pl.Chunks[i].Chunk) // Запускаем получение
			}
		}
		// Немного простоя
		p, _ := time.ParseDuration("100ms")
		time.Sleep(p)
	}
}

func (r *Reader) Work() {
	go r.updatePlaylist()
	go r.readChunks()
}

func (r *Reader) PrintStat() {
	fmt.Printf(i18n.Translation.GetText(MSG_READER_STAT), r.ID)
	fmt.Printf(i18n.Translation.GetText(MSG_STAT_REQ), r.St.connections)
	fmt.Printf(i18n.Translation.GetText(MSG_STAT_ERR), r.St.errors)
	fmt.Printf(i18n.Translation.GetText(MSG_STAT_BYTES), r.St.bytesReceived)
	speed := float64(r.St.bytesReceived) * 8.0 / r.St.sec.Seconds() / (1024 * 1024)
	fmt.Printf(i18n.Translation.GetText(MSG_STAT_SPEED), fmt.Sprintf("%.2f", speed))
}

func IfThenElse(condition bool, a interface{}, b interface{}) interface{} {
	if condition {
		return a
	}
	return b
}

func (r *Reader) PrintPlaylist() {
	fmt.Printf(i18n.Translation.GetText(MSG_PL), r.ID)
	for i := 0; i < len(r.Pl.Chunks); i++ {
		fmt.Printf(i18n.Translation.GetText(MSG_PL_CHUNK), r.Pl.Chunks[i].Chunk,
			IfThenElse(r.Pl.Chunks[i].Loaded, i18n.Translation.GetText(MSG_YES),
				IfThenElse(r.Pl.Chunks[i].Locked, i18n.Translation.GetText(MSG_NO_LOADED), i18n.Translation.GetText(MSG_NO))))
	}
}
