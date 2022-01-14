package main

/*
int busywork(int iterations) {
	int x = 0xdeadbeef;
	for (int i = 0; i < iterations; i++) {
		x ^= i;
		x *= 42;
		x += x % 1234567;
		x = (x << 3) | (x >> 7);
	}
	return x;
}
*/
import "C"

type CGo struct {}

const DefaultCGoIters = 4_000_000

func (CGo) Setup() error { return nil}

func (CGo) Run() error {
	var n C.int = DefaultCGoIters
	C.busywork(n)
	return nil
}
