package main

import (
	"flag"
	"fmt"
	"github.com/orensnake/i18n"
	"runtime"
	"time"
)

var (
	WORKERS       int    = 1                                                                                           // кол-во "потоков"
	REPORT_PERIOD int    = 10                                                                                          // частота отчетов (сек)
	WORKTIME      int    = 60                                                                                          // секунд до остановки
	URL           string = "http://127.0.0.1:8081/stream/2x2/hls/playlist.m3u8?st=QTTKk4yW8Ws6cQclK2qsHQ&e=1634068066" // адрес потока
)

var Readers []*Reader

const (
	MSG_WORKERS    = 1
	MSG_REPORTTIME = 2
	MSG_WORKTIME   = 3
	MSG_URL        = 4
	MSG_DONE       = 5
)

func init() {

	i18n.Translation = new(i18n.TTranslation)
	i18n.Translation.Init("translation.json")
}

func test() {
	// Создаем читалки
	for i := 0; i < WORKERS; i++ {
		Readers = append(Readers, new(Reader))
		// Инициализируем
		Readers[i].Init(URL, i)
	}

	// Запускаем читалки (параллельно)
	for i := 0; i < WORKERS; i++ {
		go Readers[i].Work()
	}

	fmt.Printf(i18n.Translation.GetText(6), WORKERS)
	t1 := time.Now() // Для определения моменты выхода

	work := true
	for work {
		t2 := time.Now()
		diff := t2.Sub(t1)
		if to, _ := time.ParseDuration(fmt.Sprintf("%vs", WORKTIME)); diff > to {
			// Если прошло TIMEOUT секунд - останавливаем процесс чтения
			for i := 0; i < WORKERS; i++ {
				Readers[i].Stop()
			}
			work = false // Ставим маркер необходимости выхода
		} else {
			t, _ := time.ParseDuration(fmt.Sprintf("%vs", REPORT_PERIOD))
			time.Sleep(t)
			for i := 0; i < WORKERS; i++ {
				Readers[i].PrintStat()     // Печатаем статистику ридера
				Readers[i].PrintPlaylist() // Печатаем инфу о текущем плейлисте
			}
		}
	}

}

func main() {
	flag.IntVar(&WORKERS, "w", WORKERS, i18n.Translation.GetText(MSG_WORKERS))
	flag.IntVar(&REPORT_PERIOD, "r", REPORT_PERIOD, i18n.Translation.GetText(MSG_REPORTTIME))
	flag.IntVar(&WORKTIME, "t", WORKTIME, i18n.Translation.GetText(MSG_WORKTIME))
	flag.StringVar(&URL, "u", URL, i18n.Translation.GetText(MSG_URL))
	//И запускаем разбор аргументов
	flag.Parse()

	// Разрешаем использовать все ядра процессора
	runtime.GOMAXPROCS(runtime.NumCPU())

	test()

	fmt.Println(i18n.Translation.GetText(MSG_DONE))
}
