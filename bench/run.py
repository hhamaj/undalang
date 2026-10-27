# compares C, Unda native, Python and the Unda interpreter on the programs in
# this directory. run from the repo root: python3 bench/run.py
import os, shutil, subprocess, sys, tempfile, time

here = os.path.dirname(os.path.abspath(__file__))
programs = ["fib", "primes", "collections", "words", "hello"]
slow = {"words"}


def best(cmd, runs):
    times, out = [], None
    for _ in range(runs):
        start = time.perf_counter()
        r = subprocess.run(cmd, capture_output=True, text=True)
        times.append(time.perf_counter() - start)
        if r.returncode != 0:
            sys.exit(f"{' '.join(cmd)} failed:\n{r.stderr}")
        out = r.stdout.strip()
    return min(times) * 1000, out


def main():
    cc = os.environ.get("CC", "cc")
    tmp = tempfile.mkdtemp(prefix="unda-bench-")
    unda = os.path.join(tmp, "unda")
    subprocess.run(["go", "build", "-o", unda, "./cmd/unda"], check=True)
    print(f"{'benchmark':<12} {'C':>9} {'native':>9} {'python':>9} {'interp':>9}   (ms)")
    for name in programs:
        src = os.path.join(here, name)
        runs = 3 if name in slow else 7
        row = {}
        if os.path.exists(src + ".c"):
            subprocess.run([cc, "-O2", "-o", f"{tmp}/{name}.c.bin", src + ".c"], check=True)
            row["C"] = best([f"{tmp}/{name}.c.bin"], runs)
        subprocess.run([unda, "build", src + ".unda", "-o", f"{tmp}/{name}.native"], check=True)
        row["native"] = best([f"{tmp}/{name}.native"], runs)
        row["python"] = best([sys.executable, src + ".py"], runs)
        row["interp"] = best([unda, "run", src + ".unda"], 1 if name in slow else 3)
        outputs = {k: v[1] for k, v in row.items()}
        if len(set(outputs.values())) != 1:
            sys.exit(f"{name}: outputs differ: {outputs}")
        cells = [f"{row[k][0]:9.1f}" if k in row else f"{'-':>9}" for k in ("C", "native", "python", "interp")]
        print(f"{name:<12} {' '.join(cells)}")
    shutil.rmtree(tmp)


main()
