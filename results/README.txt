This is the result of running several benchmarks on an AWS EC2 c5.4xlarge
instance. The tests were run with and without cpu profiling, and with and
without concurrency.

The following files contain the output of the tests:

* all.withgc.csv
	Ran every test for 20s, 10 times, with and without CPU profiling,
	and with 1 or 8 goroutines. GC was enabled.
* all.nogc.csv
	Ran every test for 5s, 10 times, with and without CPU profiling,
	and with 1 or 8 goroutines. GC was disabled for investigating
	a performance improvement in the JSON benchmark with CPU profiling
	enabled. The duration was set to 5s because the benchmark exhausted
	the memory on my machine when I set it to 20s.
* all.nogc.httplong.csv
	all.nogc.csv, but ran the HTTP benchmark for 20s. With only 5s, I
	saw a somewhat bi-modal distribution in the number of iterations.

cpuinfo and os-release are just to document what kind of hardware/OS I
had for testing.
