#include <stdio.h>
#include <stdint.h>
#include <stdlib.h>
int main(void) {
    size_t n = 1000000, k = 0;
    int64_t *xs = malloc(n * sizeof *xs), *ys = malloc(n * sizeof *ys), *zs = malloc(n * sizeof *zs);
    for (size_t i = 0; i < n; i++) xs[i] = (int64_t)i;
    for (size_t i = 0; i < n; i++) ys[i] = xs[i] * 2;
    for (size_t i = 0; i < n; i++) if (ys[i] % 3 == 0) zs[k++] = ys[i];
    int64_t acc = 0;
    for (size_t i = 0; i < k; i++) acc += zs[i];
    printf("%lld\n", (long long)acc);
    free(xs); free(ys); free(zs);
    return 0;
}
