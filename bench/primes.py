count = 0
n = 2
while n < 300000:
    d = 2
    prime = True
    while d * d <= n:
        if n % d == 0:
            prime = False
            break
        d += 1
    if prime:
        count += 1
    n += 1
print(count)
