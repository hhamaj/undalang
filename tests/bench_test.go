package tests

import (
	"io"
	"testing"

	"undalang/runner"
)

// interpreter benchmarks, run with go test ./tests -bench . -run '^$'
var benchPrograms = map[string]string{
	"fib": `fn fib(n) { if n < 2 { return n }
return fib(n - 1) + fib(n - 2) }
fib(22)`,
	"loops": `let mut count = 0
let mut n = 2
while n < 20000 {
    let mut d = 2
    let mut prime = true
    while d * d <= n {
        if n % d == 0 { prime = false
break }
        d = d + 1
    }
    if prime { count = count + 1 }
    n = n + 1
}`,
	"collections": `import "arrays" as arrays
let xs = arrays.range(0, 100000)
let picked = arrays.filter(arrays.map(xs, fn(x) { x * 2 }), fn(x) { x % 3 == 0 })
arrays.reduce(picked, 0, fn(acc, x) { acc + x })`,
}

func BenchmarkInterpreter(b *testing.B) {
	for name, src := range benchPrograms {
		b.Run(name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if code := runner.RunSource(src, io.Discard, io.Discard); code != 0 {
					b.Fatalf("exit %d", code)
				}
			}
		})
	}
}
