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
	Low:           flag.Int("l", 1, "RPS value to start test with"),
	High:          flag.Int("h", 60, "RPS value to finish test with"),
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

// RPSAt calculates an RPS value which should be generated at `t` since start.
func (l *Lancer) RPSAt(t time.Duration) float64 {
	return l.low + (l.high-l.low)*float64(t)/float64(l.duration)
}

// Next calculates the interval to sleep after the request sent at `t` since start.
func (l *Lancer) Next(t time.Duration) time.Duration {
	return time.Duration(float64(time.Second) / l.RPSAt(t))
}

// Start to simulate load with ticks sent to lance channel.
func (l *Lancer) Start(lance chan struct{}) {
	t := time.Duration(0)
	start := time.Now()
	finish := start.Add(l.duration)
	for finish.After(time.Now()) {
		lance <- struct{}{}
		t += l.Next(t)
		dt := start.Add(t).Sub(time.Now())
		if dt < 0 {
			log.Printf("Missed time for lance at %s", t)
			continue
		}
		time.Sleep(dt)
	}
}

func main() {

	flag.Parse()

	lancer := NewLancer(float64(*C.Low), float64(*C.High), *C.Duration)
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

	lancer.Start(lance)

}
