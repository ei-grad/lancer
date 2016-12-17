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

// Parse access.log file, construct http.Request objects and put them to
// spears channel
func Parse(filename string, spears chan *http.Request) {
	f, err := os.Open(*C.AccessLogFile)
	if err != nil {
		log.Printf("Can't open access log file: %s", err)
		return
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		parts := strings.Split(line, " ")
		fmt.Printf("%+v\n", parts)
		method := parts[5][1:]
		path := parts[6]
		req, err := http.NewRequest(method, fmt.Sprintf("%s://%s%s", *C.Scheme, *C.Target, path), nil)
		if err != nil {
			log.Printf("NewRequest failed: %s", err)
			continue
		}
		spears <- req
	}
	if s.Err() != nil {
		log.Printf("Error reading %s: %s", filename, s.Err())
	}
}

// Lancer generates linearly increasing load of HTTP requests
type Lancer struct {
	low, high float64
	duration  time.Duration
}

// NewLancer creates a new Lancer object
func NewLancer(low, high float64, duration time.Duration) *Lancer {
	return &Lancer{
		low:      low,
		high:     high,
		duration: duration,
	}
}

// RPSAt calculates an RPS value which should be generated at `t` since load generation start.
func (l *Lancer) RPSAt(t time.Duration) float64 {
	return l.low + (l.high-l.low)*float64(t)/float64(l.duration)
}

// Count of requests to be sent
func (l *Lancer) Count() int {
	return int((l.low + l.high) / 2 * l.duration.Seconds())
}

func main() {

	flag.Parse()

	lancer := NewLancer(float64(*C.High), float64(*C.Low), *C.Duration)
	count := lancer.Count()
	times := make([]time.Duration, count)
	times[count-1] = *C.Duration
	for i := count - 2; i >= 0; i-- {
		times[i] = times[i+1] - time.Duration(float64(time.Second)/lancer.RPSAt(times[i+1]))
	}

	spears := make(chan *http.Request)

	go Parse(*C.AccessLogFile, spears)

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

}
