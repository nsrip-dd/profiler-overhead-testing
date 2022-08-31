// An alternative version of the Go observability benchmark found at
// github.com/felixge/go-observability-bench.
//
// The main difference is the output format. This version outputs CSV records,
// intended to be concatenated together and fed straight into a table-based
// analysis tool like R (read_csv), Pandas (pd.read_csv), or SQLite3 (.mode csv;
// .import <filename>). There you can do any filtering, transformation, grouping,
// summarizing, etc.
//
// I also chose to report the benchmark times over the whole benchmark, rather
// than per-iteration. This is how the Go benchmark tool does it. Though I'm not
// sure which way is better TBH.
//
// It also is intended to take everything from the command line, with maybe a
// wrapper script to orchestrate multiple runs.
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/felixge/go-observability-bench/workload"
	"github.com/pkg/profile"
)

var usingCgotraceback bool

// ByteCounter is an io.Writer which records how many bytes have been written.
type ByteCounter int64

func (b *ByteCounter) Write(p []byte) (int, error) {
	*b += ByteCounter(len(p))
	return len(p), nil
}

func main() {
	duration := flag.String("duration", "1s", "length of benchmark (in Go time.Duration format")
	benchmark := flag.String("benchmark", "http", "name of benchmark")
	repeat := flag.Int("repeat", 1, "how many times to repeat benchmark")
	profiles := flag.String("profiles", "none", "semicolon-separated list of profiles")
	header := flag.Bool("header", true, "print CSV header")
	concurrency := flag.Int("concurrency", 1, "how many concurrent goroutines to run benchmark")
	ballast := flag.Int("ballast", 0, "> 1 to allocate some extra memory (for GC testing)")
	mem := flag.Bool("mem", false, "record memory profile")
	memstat := flag.Bool("memstat", false, "report memory stats at program exit")
	flag.Parse()

	ballastch := make(chan []byte)
	if *ballast > 0 {
		// A "ballast" is basically a trick to keep the in-use heap
		// memory higher. GC is triggered by heap growth, and the
		// bigger the heap, the bigger the next target heap size for GC
		// will be. So with a ballast making the heap large, GC will
		// trigger less frequently provided the rest of our application
		// doesn't allocate that much.
		//
		// I use a channel here, and receive from it at the end of the
		// program, so that the memory actually sticks around.
		go func() {
			memballast := make([]byte, *ballast)
			ballastch <- memballast
		}()
	}

	if len(os.Getenv("DISABLE_MEM_PROFILE")) > 0 {
		runtime.MemProfileRate = 0
	}
	if *mem {
		defer profile.Start(profile.MemProfile, profile.ProfilePath(".")).Stop()
	}

	var w workload.Workload
	switch *benchmark {
	case "json":
		w = &workload.JSON{File: "data/small.json"}
	case "http":
		w = &workload.HTTP{}
	case "cgo":
		w = CGo{}
	default:
		fmt.Fprintf(os.Stderr, "unrecognized test %s\n", *benchmark)
		return
	}

	d, err := time.ParseDuration(*duration)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bad time format: %s\n", err)
		return
	}

	enabledProfs := strings.Split(*profiles, ";")
	sort.Strings(enabledProfs)
	*profiles = strings.Join(enabledProfs, ";")

	if *header {
		fmt.Println("name,iters,ns,cpu-ns,profiles,profile-bytes,concurrency,using-cgotraceback")
	}
	for i := 0; i < *repeat; i++ {
		bc := new(ByteCounter)
		for _, prof := range enabledProfs {
			switch prof {
			case "cpu":
				pprof.StartCPUProfile(bc)
			}
		}

		var (
			res Result
			err error
		)

		res, err = ConcurrentRunner(w, *benchmark, d, *concurrency)
		if err != nil {
			fmt.Fprintf(os.Stderr, "discarding failed test: %s\n", err)
			continue
		}
		res.Profiles = *profiles
		res.Concurrency = *concurrency

		for _, prof := range enabledProfs {
			switch prof {
			case "cpu":
				pprof.StopCPUProfile()
			}
		}
		res.ProfileBytes = int64(*bc)
		fmt.Printf("%s\n", strings.Join(res.ToRecord(), ","))
	}

	if *memstat {
		var mstat runtime.MemStats
		runtime.ReadMemStats(&mstat)
		fmt.Fprintf(os.Stderr, "pause-ns: %+v, total-alloc: %v, num-gc: %v\n", mstat.PauseTotalNs, mstat.TotalAlloc, mstat.NumGC)
		var rusage syscall.Rusage
		syscall.Getrusage(0, &rusage)
		fmt.Fprintf(os.Stderr, "max-rss: %d\n", rusage.Maxrss)
	}
	if *ballast > 0 {
		s := <-ballastch
		fmt.Fprintln(os.Stderr, "ballast size:", len(s))
	}
}

// Result is the information collected after running a benchmark.
// Modeled after testing.BenchmarkResult
type Result struct {
	// Name is the particular benchmark, e.g. http or json
	Name string
	// N is the number of iterations
	N int
	// T is the total elapsed time
	T time.Duration
	// CPUTime is the total CPU time, including user and system time
	CPUTime time.Duration
	// Profiles is a semicolon-separated list of profiles which were enabled
	Profiles string
	// ProfileBytes is how much profiling data was recorded.
	ProfileBytes int64
	// Concurrency is how many goroutines were running the benchmark
	Concurrency int
}

// ToRecord encodes the Result as a CSV record
func (r Result) ToRecord() []string {
	return []string{
		r.Name,
		strconv.FormatInt(int64(r.N), 10),
		strconv.FormatInt(r.T.Nanoseconds(), 10),
		strconv.FormatInt(r.CPUTime.Nanoseconds(), 10),
		r.Profiles,
		strconv.FormatInt(r.ProfileBytes, 10),
		strconv.FormatInt(int64(r.Concurrency), 10),
		strconv.FormatBool(usingCgotraceback),
	}
}

// ConcurrentRunner repeatedly calls w.Run() until the given duration has
// elapsed. Returns a result with the timing and iteration information
// populated
func ConcurrentRunner(w workload.Workload, name string, duration time.Duration, concurrency int) (res Result, err error) {
	done := make(chan struct{})
	time.AfterFunc(duration, func() { close(done) })

	type runInfo struct {
		iters   int
		elapsed time.Duration
		err     error
	}
	ch := make(chan runInfo, concurrency)
	run := func() {
		var n int
		var elapsed time.Duration
		var info runInfo
		err = w.Setup()
		if err != nil {
			info.err = err
			ch <- info
			return
		}
		for {
			start := time.Now()
			err = w.Run()
			if err != nil {
				info.err = err
				ch <- info
				return
			}
			elapsed += time.Since(start)
			n++
			select {
			case <-done:
				info.iters = n
				info.elapsed = elapsed
				ch <- info
				return
			default:
			}
		}
	}
	before := CPURusage()
	for i := 0; i < concurrency; i++ {
		go run()
	}
	res = Result{
		Name: name,
	}
	for i := 0; i < concurrency; i++ {
		info := <-ch
		res.N += info.iters
		res.T += info.elapsed
		if info.err != nil {
			fmt.Fprintln(os.Stderr, "bench failed early with error %s\n", info.err)
		}
	}
	res.CPUTime = CPURusage() - before

	return res, nil
}

// CPURusage reports the total elapsed CPU time scheduled to the process,
// including user and system time.
func CPURusage() time.Duration {
	var r syscall.Rusage
	syscall.Getrusage(0, &r)
	return tvtotd(r.Stime) + tvtotd(r.Utime)
}

func tvtotd(t syscall.Timeval) time.Duration {
	return time.Duration(t.Nano())
}
