# goprof

This library provides single entry point to all profiling functionality available in golang 1.5. 
`StartProfiling` starts writing [trace](https://golang.org/cmd/trace/) and [cpu profile](https://golang.org/pkg/runtime/pprof/#StartCPUProfile) to some random directory it creates before running.
When you call `StopProfiling` it writes [heap profile](https://golang.org/pkg/runtime/pprof/#WriteHeapProfile) to the same directory as well as stopping current profiling.
By default, `StartProfiling` writes profiles up to 5 minutes in order to avoid forgotten profiling.
## Code example
```
http.HandleFunc("/", index)
	// we start profiling tools at a separate address in background
	go func() {
		profilingAddress := ":8033"
		fmt.Printf("Running profiling tools on %v\n", profilingAddress)
		if err := goprof.ListenAndServe(profilingAddress); err != nil {
			panic(err)
		}
	}()
```

## Logging

By default, the library writes logs about start/stop profiling and errors using standard go logger. You can provide
your own log function in order to make it fit your logging or shut up the logging at all.

We don't use much log levels since all the messages have quite the same level.

## License

MIT