counts = {}
i = 0
while i < 200000:
    w = "w" + str(i % 1000)
    c = counts.get(w)
    counts[w] = 1 if c is None else c + 1
    i += 1
print(len(counts), counts["w7"])
