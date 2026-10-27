from functools import reduce
xs = list(range(0, 1000000))
picked = list(filter(lambda x: x % 3 == 0, map(lambda x: x * 2, xs)))
print(reduce(lambda acc, x: acc + x, picked, 0))
