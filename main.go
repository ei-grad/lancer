package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"golang.org/x/sync/errgroup"
	"io/ioutil"
	"log"
	"math"
	"net/http"
	"os"
	"strings"
	"sync"
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
		url := fmt.Sprintf("%s://%s%s", scheme, target, path)
		req, err := http.NewRequest(method, url, nil)
		if err != nil {
			return fmt.Errorf("can't construct request: %s", err)
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

	lowSq, slope, durationSeconds float64
}

// NewLancer creates a new Lancer object
func NewLancer(low, high float64, duration time.Duration) *Lancer {
	durationSeconds := float64(duration) / float64(time.Second)
	return &Lancer{
		low:             low,
		high:            high,
		duration:        duration,
		lowSq:           low * low,
		slope:           (high - low) / durationSeconds,
		durationSeconds: durationSeconds,
	}
}

func (l *Lancer) tickTime(n int) time.Duration {
	if l.slope == 0 {
		return time.Duration(float64(n*int(time.Second)) / l.low)
	}
	ret := (math.Sqrt(l.lowSq+2*l.slope*float64(n)) - l.low) / l.slope
	return time.Duration(ret * float64(time.Second))
}

// Lance starts a load simulation with sending ticks to lance channel.
func (l *Lancer) Lance(ctx context.Context, lance chan struct{}) error {
	count := int((l.high + l.low) * l.durationSeconds / 2)
	start := time.Now()
	select {
	case lance <- struct{}{}:
	case <-ctx.Done():
		return nil
	}
	for i := 1; i < count+1; i++ {
		tickTime := l.tickTime(i)
		dt := start.Add(tickTime).Sub(time.Now())
		if dt < 0 {
			return fmt.Errorf("missed time for lance near %.1f RPS",
				float64(time.Second)/float64(l.tickTime(i)-l.tickTime(i-1)))
		}
		time.Sleep(dt)
		select {
		case lance <- struct{}{}:
		case <-ctx.Done():
			return nil
		}
	}
	return nil
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
		t := time.Now()
		resp, err := http.DefaultTransport.RoundTrip(spear)
		if err != nil {
			hits <- Hit{
				Timestamp: t,
				Error:     err,
			}
			return nil
		}
		body, err := ioutil.ReadAll(resp.Body)
		// TODO: use httptrace module to get additional info
		hits <- Hit{
			Timestamp: t,
			ProtoCode: resp.StatusCode,
			TotalTime: time.Now().Sub(t),
			SizeIn:    len(body),
		}
	}
	return nil
}

func main() {

	low := flag.Int("l", 0, "RPS value to start test with")
	high := flag.Int("h", 60, "RPS value to finish test with")
	duration := flag.Duration("d", time.Minute, "test duration")
	filename := flag.String("f", "access.log", "access.log file location")
	target := flag.String("t", "localhost", "target")
	scheme := flag.String("s", "http", "scheme")

	flag.Parse()

	spears := make(chan *http.Request)
	lance := make(chan struct{})
	hits := make(chan Hit, 10000)

	ctx, stop := context.WithCancel(context.Background())
	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		defer close(spears)
		return Parse(ctx, *filename, *scheme, *target, spears)
	})

	g.Go(func() error {
		defer close(hits)
		var wg sync.WaitGroup
		for i := 0; i < 10240; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				Worker(ctx, spears, lance, hits)
			}()
		}
		wg.Wait()
		return nil
	})

	g.Go(func() error {
		// TODO: influxdb output
		// TODO: phout output
		// TODO: overload.yandex.ru output
		for hit := range hits {
			fmt.Printf("%+v\n", hit)
		}
		return nil
	})

	g.Go(func() error {
		defer stop()
		defer close(lance)
		lancer := NewLancer(float64(*low), float64(*high), *duration)
		return lancer.Lance(ctx, lance)
	})

	err := g.Wait()
	if err != nil {
		log.Fatal(err)
	}

}
