package main

import (
	"bufio"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// C stands for Config or Context
var C = struct {
	Low, High *int
	Duration  *time.Duration

	AccessLogFile, Target, Scheme *string
}{
	Low:           flag.Int("low", 1, "RPS value to start test with"),
	High:          flag.Int("high", 60, "RPS value to finish test with"),
	Duration:      flag.Duration("d", time.Minute, "test duration"),
	AccessLogFile: flag.String("f", "access.log", "access.log file location"),
	Target:        flag.String("t", "localhost", "target"),
	Scheme:        flag.String("s", "http", "scheme"),
}

func main() {

	flag.Parse()

	High := float64(*C.High)
	Low := float64(*C.Low)
	Duration := *C.Duration

	f, err := os.Open(*C.AccessLogFile)
	if err != nil {
		log.Fatalf("Can't open access log file: %s", err)
	}
	defer f.Close()
	s := bufio.NewScanner(f)

	seconds := C.Duration.Seconds()
	count := int((Low + High) / 2 * seconds)
	fmt.Printf("Total request count: %d\n", count)
	times := make([]time.Duration, count)

	rpsAt := func(t time.Duration) float64 {
		return Low + (High-Low)*float64(t)/float64(Duration)
	}

	times[count-1] = Duration
	for i := count - 2; i >= 0; i-- {
		times[i] = times[i+1] - time.Duration(float64(time.Second)/rpsAt(times[i+1]))
	}

	fmt.Printf("Request schedule: %+v\n", times)

	spears := make(chan *http.Request)

	go func() {
		for s.Scan() {
			line := s.Text()
			parts := strings.Split(line, " ")
			fmt.Printf("%+v\n", parts)
			method := parts[5][1:]
			path := parts[6]
			req, err := http.NewRequest(method, fmt.Sprintf("%s://%s%s", *C.Scheme, *C.Target, path), nil)
			if err != nil {
				log.Fatalf("NewRequest failed: %s", err)
			}
			spears <- req
		}
		if s.Err() != nil {
			// XXX: We may want to dump our current state and exit gracefully at this point.
			log.Println("Error reading STDIN: ", s.Err())
		}
	}()

	lance := make(chan struct{})

	type Hit map[string]interface{}

	hits := make(chan Hit)

	go func() {
		defer close(hits)
		for spear := range spears {
			<-lance
			go func(spear *http.Request) {
				tStart := time.Now()
				resp, err := http.DefaultTransport.RoundTrip(spear)
				if err != nil {
					hits <- Hit{
						"timestamp": tStart,
						"status":    599,
						"error":     err.Error(),
					}
					return
				}
				tHead := time.Now()
				body, err := ioutil.ReadAll(resp.Body)
				tAll := time.Now()
				hits <- Hit{
					"timestamp":   tStart,
					"status":      resp.Status,
					"time_head":   tHead.Sub(tStart),
					"time_body":   tAll.Sub(tStart),
					"body_length": len(body),
				}
			}(spear)
		}
	}()

	finished := make(chan struct{})

	go func() {
		for hit := range hits {
			fmt.Printf("%+v\n", hit)
		}
		close(finished)
	}()

	start := time.Now()
	for i := 0; i < count; i++ {
		dt := start.Add(times[i]).Sub(time.Now())
		if dt < 0 {
			log.Printf("Missed time for lance %d", i)
			continue
		}
		time.Sleep(dt)
		lance <- struct{}{}
	}

	// terminate the scanner
	f.Seek(0, 2)
	<-spears
	// XXX: broken teardown
	close(spears)
	close(lance)

	// wait for pending requests to be send and statistics to be saved
	<-finished

}
