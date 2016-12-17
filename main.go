package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"golang.org/x/sync/errgroup"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// Parse access.log file, construct http.Request objects and put them to
// spears channel
func Parse(ctx context.Context, filename, scheme, target string, spears chan *http.Request) error {
	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		parts := strings.Split(line, " ")
		// TODO: check method and path validity
		method := parts[5][1:]
		path := parts[6]
		req, err := http.NewRequest(method, fmt.Sprintf("%s://%s%s", scheme, target, path), nil)
		if err != nil {
			log.Printf("NewRequest failed: %s", err)
			continue
		}
		select {
		case spears <- req:
		case <-ctx.Done():
			return nil
		}
	}
	if s.Err() != nil {
		return s.Err()
	}
	return nil
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

// RPSAt calculates an RPS value at `t`.
func (l *Lancer) RPSAt(t time.Duration) float64 {
	return l.low + (l.high-l.low)*float64(t)/float64(l.duration)
}

// Next calculates the interval between `t` and the next request.
func (l *Lancer) Next(t time.Duration) time.Duration {
	return time.Duration(float64(time.Second) / l.RPSAt(t))
}

// Lance starts a load simulation with sending ticks to lance channel.
func (l *Lancer) Lance(lance chan struct{}) {
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

// Hit contains info about request timings, sizes and statuses
// TODO: add ConnectTime, SendTime, ReceiveTime, SizeOut, NetCode
type Hit struct {
	Timestamp         time.Time
	TotalTime         time.Duration
	SizeIn, ProtoCode int
	Error             error
}

// Worker sends an http.Requests coming from spears channel
func Worker(ctx context.Context, spears chan *http.Request, lance chan struct{}, hits chan Hit) error {
	for spear := range spears {
		select {
		case <-lance:
		case <-ctx.Done():
			return nil
		}
		// TODO: get rid of creating a goroutine per request, use a dynamic
		// pool of workers
		go func(spear *http.Request) {
			t := time.Now()
			resp, err := http.DefaultTransport.RoundTrip(spear)
			if err != nil {
				hits <- Hit{
					Timestamp: t,
					Error:     err,
				}
				return
			}
			body, err := ioutil.ReadAll(resp.Body)
			// TODO: get additional info from transport layer
			hits <- Hit{
				Timestamp: t,
				ProtoCode: resp.StatusCode,
				TotalTime: time.Now().Sub(t),
				SizeIn:    len(body),
			}
		}(spear)
	}
	return nil
}

func main() {

	low := flag.Int("l", 1, "RPS value to start test with")
	high := flag.Int("h", 60, "RPS value to finish test with")
	duration := flag.Duration("d", time.Minute, "test duration")
	filename := flag.String("f", "access.log", "access.log file location")
	target := flag.String("t", "localhost", "target")
	scheme := flag.String("s", "http", "scheme")

	flag.Parse()

	spears := make(chan *http.Request)
	defer close(spears)

	lance := make(chan struct{})
	defer close(lance)

	hits := make(chan Hit)
	defer close(hits)

	ctx, stop := context.WithCancel(context.Background())
	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error { return Parse(ctx, *filename, *scheme, *target, spears) })

	g.Go(func() error { Worker(ctx, spears, lance, hits); return nil })

	g.Go(func() error {
		// TODO: influxdb output
		// TODO: phout output
		// TODO: overload.yandex.ru output
		for hit := range hits {
			fmt.Printf("%+v\n", hit)
		}
		return nil
	})

	NewLancer(float64(*low), float64(*high), *duration).Lance(lance)

	stop()

	err := g.Wait()
	if err != nil {
		log.Fatal(err)
	}

}
