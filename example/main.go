package main

import (
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"sync"

	"github.com/lazada/goprof"
)

// this is just a dummy handler which runs a bunch of goroutines which just check if random numbers are prime or not
func index(w http.ResponseWriter, r *http.Request) {
	numbers, primes, composites := make(chan int), make(chan int), make(chan int)
	wg := sync.WaitGroup{}
	for i := 0; i < 11; i++ {
		go func() {
			wg.Add(1)
			for number := range numbers {
				if isPrime(number) {
					primes <- number
				} else {
					composites <- number
				}
			}
			wg.Done()
		}()
	}

	go func() {
		wg.Wait()
		close(primes)
		close(composites)
	}()

	go func() {
		count, _ := strconv.Atoi(r.URL.Query().Get("numbers"))
		if count <= 0 {
			count = 37
		}
		for i := 0; i < count; i++ {
			number := rand.Int() % 10000000
			if number < 0 {
				number = -number
			}
			numbers <- number
		}
		close(numbers)
	}()

	for {
		primeOk, compositeOk := true, true
		select {
		case prime, ok := <-primes:
			if ok {
				fmt.Fprintf(w, "Number %d is prime\n", prime)
				continue
			}
			primeOk = false
		default:
		}
		select {
		case composite, ok := <-composites:
			if ok {
				fmt.Fprintf(w, "Number %d is composite\n", composite)
				continue
			}
			compositeOk = false
		default:
		}
		if !primeOk && !compositeOk {
			return
		}
		select {
		case prime, ok := <-primes:
			if !ok {
				continue
			}
			fmt.Fprintf(w, "Number %d is prime\n", prime)
		case composite, ok := <-composites:
			if !ok {
				continue
			}
			fmt.Fprintf(w, "Number %d is composite\n", composite)
		}
	}
}

func isPrime(number int) bool {
	for divisor := 2; divisor < int(math.Sqrt(float64(number))); divisor++ {
		if number%divisor == 0 {
			return false
		}
	}
	return true
}

func main() {
	http.HandleFunc("/", index)
	// we start profiling tools at a separate address in background
	go func() {
		profilingAddress := ":8033"
		fmt.Printf("Running profiling tools on %v\n", profilingAddress)
		if err := goprof.ListenAndServe(profilingAddress); err != nil {
			panic(err)
		}
	}()
	appAddress := ":8032"
	fmt.Printf("Running app on %v\n", appAddress)
	if err := http.ListenAndServe(appAddress, nil); err != nil {
		panic(err)
	}
}
