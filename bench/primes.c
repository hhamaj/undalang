#include <stdio.h>
#include <stdint.h>
#include <stdbool.h>
int main(void) {
    int64_t count = 0;
    for (int64_t n = 2; n < 300000; n++) {
        bool prime = true;
        for (int64_t d = 2; d * d <= n; d++) if (n % d == 0) { prime = false; break; }
        if (prime) count++;
    }
    printf("%lld\n", (long long)count);
    return 0;
}
