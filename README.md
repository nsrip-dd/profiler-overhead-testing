These are notes on my first Datadog research week, Jan 10-14 2022. I studied
the effects of enabling the Go CPU profiler on CPU usage, benchmark latency,
and memory usage. Building off of Felix’s [work](https://github.com/felixge/go-observability-bench) which he [presented](https://www.gophercon.com/agenda/session/596212) at
GopherCon 2021.

* Benchmarking different workloads in a loop, measuring
    * Total time spent in the workload -> average time per iteration
    * CPU time used (system and user)
    * Number of concurrent goroutines running the benchmark (configurable)
    * Byte of profiler data produced
        * I didn’t spend as much time studying this in particular.
* There were three workloads I tried out:
    * Make an HTTP GET request to a server (running in the same program) and reads the response (“hello world”)
    * Unmarshal and marshal some JSON (~4500 line randomly generated data)
    * Run a CGo function (i.e. the inner body of the function is implemented in C) that just does some busy work.
* Did testing on an AWS c5.4xlarge EC2 instance, running Ubuntu 20.04 and Go version 1.17.6. The instance had an Intel Skylake-generation CPU with 16 cores and 32GB memory.
* Ran repeatedly for 20 seconds at a time, with and without CPU profile, and with either 1 or 8 (the number of “cores”) goroutines.
* Saw the following results initially:

|name | concurrency|profiles | wall time/iter (ns)| CPU time/iter (ns)|
|:----|-----------:|:--------|--------------:|-------------:|
|cgo  |           1|cpu      |    20135840.79|    20159975.6|
|cgo  |           1|none     |    20129998.77|    20145644.9|
|cgo  |           8|cpu      |    20153188.56|    20167577.9|
|cgo  |           8|none     |    20133374.46|    20146963.8|
|http |           1|cpu      |       58607.57|      122200.8|
|http |           1|none     |       57053.75|      117743.5|
|http |           8|cpu      |      174741.15|      242928.7|
|http |           8|none     |      180817.85|      222425.6|
|json |           1|cpu      |     3013827.08|     4187814.6|
|json |           1|none     |     2916262.13|     3611406.0|
|json |           8|cpu      |     6594919.00|     7575878.1|
|json |           8|none     |     8367607.13|     9817775.8|
* Highlights:
    * CGo: less than 1% increase in latency and CPU time for both 1 and 8 goroutines.
    * HTTP: varied a lot depending on the duration of the benchmark. For shorter (5 seconds) saw ~8% increase in latency, for 20 seconds saw a ~4-5% decrease in latency.
    * JSON: For the ~4500 line record, I saw a surprising 20% decrease in latency with the CPU profiler turned on! For the big record, I saw no consistent difference.
* Decided to follow the faster JSON benchmark trail.
* Used Linux’s “perf” program to record call stacks while the benchmark was running, see where time was spent. Mainly noticed fewer samples in GC-related functions.
    * Also tried recording kernel scheduling events, saw a decrease in events related to GC.
* In Go’s runtime memory stats, I saw there was indeed less time spent paused for GC and fewer collection events with CPU profile turned on…
* Tried completely disabling GC, and the performance difference basically disappeared!
* Re-ran all of the benchmarks with the GC disabled
    * Had to do shorter runs since long runs with the allocation-heavy JSON workload made my EC2 instance grind to a halt…

|name | concurrency|profiles | wall time/iter (ns)| CPU time/iter (ns)|
|:----|-----------:|:--------|-------------------:|------------------:|
|cgo  |           1|cpu      |         20135629.72|         20163329.7|
|cgo  |           1|none     |         20129095.96|         20146260.6|
|cgo  |           8|cpu      |         20158140.58|         20172809.1|
|cgo  |           8|none     |         20133727.01|         20145656.3|
|http |           1|cpu      |            55837.41|           110073.4|
|http |           1|none     |            55570.44|           109756.7|
|http |           8|cpu      |           141479.97|           257726.6|
|http |           8|none     |           141561.53|           258091.4|
|json |           1|cpu      |          3352887.16|          3358657.7|
|json |           1|none     |          3343721.64|          3347449.1|
|json |           8|cpu      |          3698553.70|          3679699.2|
|json |           8|none     |          3694975.81|          3675510.1|

* Highlights:
    * With GC disabled, saw <1% increase in latency and CPU utilization across the benchmarks, except ~2% increase for HTTP workload with 1 goroutine.
    * HTTP benchmark seemed kind of “bi-modal” in terms of latency. Longer runs were more consistently <1% increase (this table has the longer runs)
* Go can log GC events. Doing this, I saw that with the CPU profiler turned on, the “heap size target” was increased. This “target” is the next heap size where Go will do garbage collection.
* So, the extra memory allocated for storing CPU profile samples managed to bump up the GC target just enough that GC events happened less frequently!
* We can reproduce this with a [“ballast”](https://blog.twitch.tv/en/2019/04/10/go-memory-ballast-how-i-learnt-to-stop-worrying-and-love-the-heap/). Basically, allocate a little extra memory that sticks around til the end of the program. In this case, a 2MB ballast was enough.
    * Side note: Memory overhead from recording CPU profiles should be limited to 1) the fixed size buffer that stores stack samples as they are captured, and 2) the hash map that accumulates the samples (stack traces with labels). In theory 2) is bounded by the number of unique stack traces. Applications with many code paths, and/or those which make heavy use of labels, may see more memory usage.
* Conclusions:
    * Overhead from the profiling activity itself seems small in these micro-benchmarks one you control for other factors, primarily garbage collection.
    * Use external tools to profile the benchmarks.
    * For benchmarking profiler overhead as part of continuous integration, would want to be aware of things like the effect on GC that might lead to misleading results.
    * Benchmarking real workloads over longer durations would likely provide more meaningful results, especially for profile memory overhead.
