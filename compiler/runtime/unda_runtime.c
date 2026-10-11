/*
 * Unda C runtime. This file is embedded by the Go compiler and prepended to
 * every generated program, so the output is a single translation unit.
 *
 * Semantics (operators, error messages, value formatting) mirror the Go
 * interpreter exactly; the differential tests in tests/native_test.go rely
 * on that. Unda values live on a garbage-collected heap (see the
 * "garbage collector" section); temporary C buffers use plain malloc.
 */
#define _GNU_SOURCE
#pragma GCC diagnostic ignored "-Wunused-function"
#include <errno.h>
#include <fcntl.h>
#include <math.h>
#include <pthread.h>
#include <setjmp.h>
#include <stdarg.h>
#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>

typedef enum { U_UNDEF = 0, U_NIL, U_BOOL, U_INT, U_FLOAT, U_STR, U_ARR, U_MAP, U_FN, U_BUILTIN, U_MOD } UTag;

typedef struct UStr { size_t len; char data[]; } UStr;
typedef struct UArr UArr;
typedef struct UMap UMap;
typedef struct UFn UFn;
typedef struct UMod UMod;

typedef struct UValue {
    UTag tag;
    union { bool b; int64_t i; double f; UStr *s; UArr *a; UMap *m; UFn *fn; UMod *mod; } as;
} UValue;

struct UArr { size_t len, cap; UValue *items; };
typedef struct { UValue key, value; } UEntry;
struct UMap { size_t len, cap; UEntry *entries; int64_t *index; size_t icap; };

typedef struct UFrame { struct UFrame *parent; UValue *slots; } UFrame;
typedef UValue (*UCode)(UFrame *env, int argc, UValue *argv);
struct UFn { UCode code; UFrame *env; int arity; const char *name; const char *inspect; };
struct UMod { const char *name; int count; const char **names; UValue *values; };

/* ---------- errors and locations ---------- */

static const char *u_file = NULL;
static int u_line = 0, u_col = 0;
static int u_depth = 0;
static int u_argc = 0;
static char **u_argv = NULL;
#define U_MAX_DEPTH 10000
#define U_LOC(f, l, c) (u_file = (f), u_line = (l), u_col = (c))

static void u_panic(const char *fmt, ...) __attribute__((noreturn, format(printf, 1, 2)));
static void u_panic(const char *fmt, ...) {
    fflush(stdout);
    fputs("runtime error: ", stderr);
    if (u_line > 0) {
        if (u_file && u_file[0]) fprintf(stderr, "%s:%d:%d: ", u_file, u_line, u_col);
        else fprintf(stderr, "line %d:%d: ", u_line, u_col);
    }
    va_list ap;
    va_start(ap, fmt);
    vfprintf(stderr, fmt, ap);
    va_end(ap);
    fputc('\n', stderr);
    fflush(stderr);
    exit(1);
}

static void u_oom(void) __attribute__((noreturn));
static void u_oom(void) {
    fflush(stdout);
    fputs("runtime error: out of memory\n", stderr);
    exit(1);
}

/* Plain allocations for temporary C buffers that are released with free(). */
static void *u_xalloc(size_t n) {
    void *p = calloc(1, n ? n : 1);
    if (!p) u_oom();
    return p;
}

static void *u_xrealloc(void *p, size_t n) {
    p = realloc(p, n ? n : 1);
    if (!p) u_oom();
    return p;
}

/* ---------- garbage collector ----------
 *
 * A conservative mark-and-sweep collector. Every Unda value is allocated
 * with u_alloc, which records the block in a table. A collection marks
 * blocks reachable from the roots, treating every aligned word as a
 * potential pointer (including pointers into the middle of a block):
 *   - the Unda thread's stack, with callee-saved registers spilled onto it,
 *   - static slots registered by the generated code (u_register_roots).
 * Unmarked blocks are freed. A collection runs once the bytes allocated
 * since the last one exceed the live heap size (minimum 8 MiB, tunable with
 * UNDA_GC_THRESHOLD).
 */

typedef struct { size_t size; size_t mark; } UGcHeader;
#define U_GC_HDR sizeof(UGcHeader)
#define U_GC_HEADER(p) ((UGcHeader *)((char *)(p) - U_GC_HDR))

typedef struct { char *start; size_t size; } UGcRoot;

static char **u_gc_blocks = NULL;
static size_t u_gc_count = 0, u_gc_cap = 0;
static char **u_gc_work = NULL;
static size_t u_gc_work_len = 0, u_gc_work_cap = 0;
static UGcRoot *u_gc_roots = NULL;
static size_t u_gc_root_count = 0;
static size_t u_gc_allocated = 0, u_gc_threshold = (size_t)8 << 20, u_gc_min = (size_t)8 << 20;
static char *u_stack_hi = NULL;
static bool u_gc_enabled = false;
static size_t u_gc_collections = 0;

static void u_gc_add_root(void *start, size_t size) {
    u_gc_roots = u_xrealloc(u_gc_roots, (u_gc_root_count + 1) * sizeof(UGcRoot));
    u_gc_roots[u_gc_root_count].start = start;
    u_gc_roots[u_gc_root_count].size = size;
    u_gc_root_count++;
}

static int u_gc_cmp(const void *a, const void *b) {
    uintptr_t x = (uintptr_t)*(char *const *)a, y = (uintptr_t)*(char *const *)b;
    return x < y ? -1 : x > y;
}

/* Returns the block containing address w (blocks must be sorted). */
static char *u_gc_find(uintptr_t w) {
    if (u_gc_count == 0 || w < (uintptr_t)u_gc_blocks[0]) return NULL;
    size_t lo = 0, hi = u_gc_count;
    while (lo < hi) {
        size_t mid = lo + (hi - lo) / 2;
        if ((uintptr_t)u_gc_blocks[mid] <= w) lo = mid + 1;
        else hi = mid;
    }
    char *b = u_gc_blocks[lo - 1];
    return w <= (uintptr_t)b + U_GC_HEADER(b)->size ? b : NULL;
}

static void u_gc_mark_range(const char *lo, const char *hi) {
    uintptr_t a = ((uintptr_t)lo + sizeof(void *) - 1) & ~(uintptr_t)(sizeof(void *) - 1);
    for (; a + sizeof(void *) <= (uintptr_t)hi; a += sizeof(void *)) {
        char *b = u_gc_find(*(const uintptr_t *)a);
        if (!b || U_GC_HEADER(b)->mark) continue;
        U_GC_HEADER(b)->mark = 1;
        if (u_gc_work_len == u_gc_work_cap) {
            u_gc_work_cap = u_gc_work_cap ? u_gc_work_cap * 2 : 1024;
            u_gc_work = u_xrealloc(u_gc_work, u_gc_work_cap * sizeof(char *));
        }
        u_gc_work[u_gc_work_len++] = b;
    }
}

static void u_gc_drain(void) {
    while (u_gc_work_len > 0) {
        char *b = u_gc_work[--u_gc_work_len];
        u_gc_mark_range(b, b + U_GC_HEADER(b)->size);
    }
}

__attribute__((noinline)) static void u_gc_scan_stack(void) {
    volatile char here = 0;
    u_gc_mark_range((const char *)&here, u_stack_hi);
    u_gc_drain();
}

__attribute__((noinline)) static void u_gc_collect(void) {
    /* Spill callee-saved registers so the stack scan sees them. */
    __builtin_unwind_init();
    jmp_buf regs;
    setjmp(regs);
    qsort(u_gc_blocks, u_gc_count, sizeof(char *), u_gc_cmp);
    u_gc_scan_stack();
    for (size_t i = 0; i < u_gc_root_count; i++) {
        u_gc_mark_range(u_gc_roots[i].start, u_gc_roots[i].start + u_gc_roots[i].size);
        u_gc_drain();
    }
    size_t kept = 0, live = 0;
    for (size_t i = 0; i < u_gc_count; i++) {
        char *b = u_gc_blocks[i];
        UGcHeader *h = U_GC_HEADER(b);
        if (h->mark) {
            h->mark = 0;
            live += h->size + U_GC_HDR;
            u_gc_blocks[kept++] = b;
        } else {
            free(h);
        }
    }
    u_gc_count = kept;
    u_gc_allocated = 0;
    u_gc_threshold = live > u_gc_min ? live : u_gc_min;
    u_gc_collections++;
}

static void *u_alloc(size_t n) {
    size_t size = (n + sizeof(void *) - 1) & ~(sizeof(void *) - 1);
    if (size == 0) size = sizeof(void *);
    if (u_gc_enabled && u_gc_allocated >= u_gc_threshold) u_gc_collect();
    UGcHeader *h = calloc(1, U_GC_HDR + size);
    if (!h && u_gc_enabled) {
        u_gc_collect();
        h = calloc(1, U_GC_HDR + size);
    }
    if (!h) u_oom();
    h->size = size;
    if (u_gc_count == u_gc_cap) {
        u_gc_cap = u_gc_cap ? u_gc_cap * 2 : 4096;
        u_gc_blocks = u_xrealloc(u_gc_blocks, u_gc_cap * sizeof(char *));
    }
    char *p = (char *)h + U_GC_HDR;
    u_gc_blocks[u_gc_count++] = p;
    u_gc_allocated += size + U_GC_HDR;
    return p;
}

/* Grows a GC block; the old block becomes garbage. */
static void *u_realloc(void *p, size_t n) {
    if (!p) return u_alloc(n);
    size_t old = U_GC_HEADER(p)->size;
    if (n <= old) return p;
    void *q = u_alloc(n);
    memcpy(q, p, old);
    return q;
}

/* ---------- string builder ---------- */

typedef struct { char *buf; size_t len, cap; } USb;

static void sb_grow(USb *sb, size_t extra) {
    if (sb->len + extra + 1 <= sb->cap) return;
    size_t cap = sb->cap ? sb->cap * 2 : 64;
    while (cap < sb->len + extra + 1) cap *= 2;
    sb->buf = u_xrealloc(sb->buf, cap);
    sb->cap = cap;
}
static void sb_putn(USb *sb, const char *s, size_t n) {
    sb_grow(sb, n);
    memcpy(sb->buf + sb->len, s, n);
    sb->len += n;
    sb->buf[sb->len] = 0;
}
static void sb_puts(USb *sb, const char *s) { sb_putn(sb, s, strlen(s)); }
static void sb_putc(USb *sb, char c) { sb_putn(sb, &c, 1); }
static void sb_printf(USb *sb, const char *fmt, ...) __attribute__((format(printf, 2, 3)));
static void sb_printf(USb *sb, const char *fmt, ...) {
    va_list ap;
    va_start(ap, fmt);
    int n = vsnprintf(NULL, 0, fmt, ap);
    va_end(ap);
    sb_grow(sb, (size_t)n);
    va_start(ap, fmt);
    vsnprintf(sb->buf + sb->len, (size_t)n + 1, fmt, ap);
    va_end(ap);
    sb->len += (size_t)n;
}

/* ---------- constructors ---------- */

static UStr *u_str_new(const char *s, size_t n) {
    UStr *str = u_alloc(sizeof(UStr) + n + 1);
    str->len = n;
    if (n) memcpy(str->data, s, n);
    str->data[n] = 0;
    return str;
}

static inline UValue uv_nil(void) { UValue v; v.tag = U_NIL; v.as.i = 0; return v; }
static inline UValue uv_bool(bool b) { UValue v; v.tag = U_BOOL; v.as.i = 0; v.as.b = b; return v; }
static inline UValue uv_int(int64_t i) { UValue v; v.tag = U_INT; v.as.i = i; return v; }
static inline UValue uv_float(double f) { UValue v; v.tag = U_FLOAT; v.as.f = f; return v; }
static inline UValue uv_strv(UStr *s) { UValue v; v.tag = U_STR; v.as.s = s; return v; }
static inline UValue uv_str_n(const char *s, size_t n) { return uv_strv(u_str_new(s, n)); }
static inline UValue uv_cstr(const char *s) { return uv_str_n(s, strlen(s)); }
static inline UValue uv_sb_value(USb *sb) { UValue v = uv_str_n(sb->buf ? sb->buf : "", sb->len); free(sb->buf); return v; }
static inline UValue uv_arrv(UArr *a) { UValue v; v.tag = U_ARR; v.as.a = a; return v; }
static inline UValue uv_mapv(UMap *m) { UValue v; v.tag = U_MAP; v.as.m = m; return v; }
static inline UValue uv_modv(UMod *m) { UValue v; v.tag = U_MOD; v.as.mod = m; return v; }

static UArr *u_arr_new(size_t cap) {
    UArr *a = u_alloc(sizeof(UArr));
    a->cap = cap;
    a->items = cap ? u_alloc(cap * sizeof(UValue)) : NULL;
    return a;
}
static void u_arr_push(UArr *a, UValue v) {
    if (a->len == a->cap) {
        a->cap = a->cap ? a->cap * 2 : 4;
        a->items = u_realloc(a->items, a->cap * sizeof(UValue));
    }
    a->items[a->len++] = v;
}

static UArr *u_arr_tail(UArr *src, size_t from) {
    UArr *a = u_arr_new(src->len - from);
    for (size_t i = from; i < src->len; i++) a->items[a->len++] = src->items[i];
    return a;
}

static UValue u_closure(UCode code, UFrame *env, int arity, const char *name, const char *inspect) {
    UFn *fn = u_alloc(sizeof(UFn));
    fn->code = code; fn->env = env; fn->arity = arity; fn->name = name; fn->inspect = inspect;
    UValue v; v.tag = U_FN; v.as.fn = fn;
    return v;
}
static UValue u_builtin(UCode code, const char *name) {
    UFn *fn = u_alloc(sizeof(UFn));
    fn->code = code; fn->arity = -1; fn->name = name; fn->inspect = "builtin function";
    UValue v; v.tag = U_BUILTIN; v.as.fn = fn;
    return v;
}

static UFrame *u_frame_new(UFrame *parent, int n) {
    UFrame *f = u_alloc(sizeof(UFrame));
    f->parent = parent;
    f->slots = u_alloc((size_t)(n > 0 ? n : 1) * sizeof(UValue));
    return f;
}
static inline UFrame *u_up(UFrame *f, int depth) {
    while (depth-- > 0) f = f->parent;
    return f;
}

/* ---------- type names and truthiness ---------- */

static const char *u_type_name(UValue v) {
    switch (v.tag) {
    case U_NIL: return "NIL";
    case U_BOOL: return "BOOLEAN";
    case U_INT: return "INTEGER";
    case U_FLOAT: return "FLOAT";
    case U_STR: return "STRING";
    case U_ARR: return "ARRAY";
    case U_MAP: return "MAP";
    case U_FN: return "FUNCTION";
    case U_BUILTIN: return "BUILTIN";
    case U_MOD: return "MODULE";
    default: return "UNDEFINED";
    }
}

static inline bool u_truthy(UValue v) {
    if (v.tag == U_NIL) return false;
    if (v.tag == U_BOOL) return v.as.b;
    return true;
}

/* ---------- UTF-8 helpers ---------- */

/* Decodes one rune like Go's utf8.DecodeRuneInString: invalid input yields
 * U+FFFD with size 1. */
static int32_t u_utf8_decode(const unsigned char *s, size_t n, int *size) {
    if (n == 0) { *size = 0; return 0xFFFD; }
    unsigned char c = s[0];
    if (c < 0x80) { *size = 1; return c; }
    int need; int32_t r; int32_t min;
    if (c >= 0xC2 && c <= 0xDF) { need = 1; r = c & 0x1F; min = 0x80; }
    else if (c >= 0xE0 && c <= 0xEF) { need = 2; r = c & 0x0F; min = 0x800; }
    else if (c >= 0xF0 && c <= 0xF4) { need = 3; r = c & 0x07; min = 0x10000; }
    else { *size = 1; return 0xFFFD; }
    if ((size_t)need + 1 > n) { *size = 1; return 0xFFFD; }
    for (int i = 1; i <= need; i++) {
        if ((s[i] & 0xC0) != 0x80) { *size = 1; return 0xFFFD; }
        r = (r << 6) | (s[i] & 0x3F);
    }
    if (r < min || r > 0x10FFFF || (r >= 0xD800 && r <= 0xDFFF)) { *size = 1; return 0xFFFD; }
    *size = need + 1;
    return r;
}

static void u_utf8_encode(USb *sb, int32_t r) {
    char b[4];
    if (r < 0 || r > 0x10FFFF || (r >= 0xD800 && r <= 0xDFFF)) r = 0xFFFD;
    if (r < 0x80) { b[0] = (char)r; sb_putn(sb, b, 1); }
    else if (r < 0x800) { b[0] = (char)(0xC0 | (r >> 6)); b[1] = (char)(0x80 | (r & 0x3F)); sb_putn(sb, b, 2); }
    else if (r < 0x10000) { b[0] = (char)(0xE0 | (r >> 12)); b[1] = (char)(0x80 | ((r >> 6) & 0x3F)); b[2] = (char)(0x80 | (r & 0x3F)); sb_putn(sb, b, 3); }
    else { b[0] = (char)(0xF0 | (r >> 18)); b[1] = (char)(0x80 | ((r >> 12) & 0x3F)); b[2] = (char)(0x80 | ((r >> 6) & 0x3F)); b[3] = (char)(0x80 | (r & 0x3F)); sb_putn(sb, b, 4); }
}

/* ---------- float formatting (Go's strconv shortest representation) ---------- */

/* Produces the shortest decimal digits that round-trip to v.
 * digits receives nd digits; the value is 0.d1d2... * 10^dp. */
static void u_shortest(double v, char *digits, int *nd, int *dp) {
    char buf[40];
    for (int prec = 0; prec < 17; prec++) {
        snprintf(buf, sizeof buf, "%.*e", prec, v);
        if (strtod(buf, NULL) == v) break;
    }
    /* buf looks like d.ddde[+-]XX */
    int n = 0;
    char *p = buf;
    for (; *p && *p != 'e'; p++) if (*p >= '0' && *p <= '9') digits[n++] = *p;
    int exp = atoi(p + 1);
    while (n > 1 && digits[n - 1] == '0') n--;
    digits[n] = 0;
    *nd = n;
    *dp = exp + 1;
    if (n == 1 && digits[0] == '0') *dp = 1;
}

static void u_fmt_e(USb *sb, const char *d, int nd, int dp) {
    sb_putc(sb, d[0]);
    if (nd > 1) { sb_putc(sb, '.'); sb_putn(sb, d + 1, (size_t)(nd - 1)); }
    int exp = (d[0] == '0') ? 0 : dp - 1;
    sb_printf(sb, "e%c%02d", exp < 0 ? '-' : '+', exp < 0 ? -exp : exp);
}

static void u_fmt_f(USb *sb, const char *d, int nd, int dp) {
    if (dp > 0) {
        for (int i = 0; i < dp; i++) sb_putc(sb, i < nd ? d[i] : '0');
    } else {
        sb_putc(sb, '0');
    }
    if (nd > dp) {
        sb_putc(sb, '.');
        for (int i = dp; i < nd; i++) sb_putc(sb, i < 0 ? '0' : d[i]);
    }
}

/* Matches fmt.Sprintf("%g", v). */
static void u_fmt_float(USb *sb, double v) {
    if (isnan(v)) { sb_puts(sb, "NaN"); return; }
    if (isinf(v)) { sb_puts(sb, v > 0 ? "+Inf" : "-Inf"); return; }
    if (signbit(v)) { sb_putc(sb, '-'); v = -v; }
    char d[40]; int nd, dp;
    u_shortest(v, d, &nd, &dp);
    int exp = (v == 0) ? 0 : dp - 1;
    /* strconv uses precision 6 for the %e decision in shortest mode. */
    int eprec = 6;
    if (exp < -4 || exp >= eprec) u_fmt_e(sb, d, nd, dp);
    else u_fmt_f(sb, d, nd, dp);
}

/* ---------- inspect ---------- */

static void u_inspect_to(USb *sb, UValue v);

static int u_cmp_sb(const void *a, const void *b) {
    const USb *x = a, *y = b;
    size_t n = x->len < y->len ? x->len : y->len;
    int c = memcmp(x->buf, y->buf, n);
    if (c) return c;
    return x->len < y->len ? -1 : x->len > y->len;
}

static int u_cmp_cstr(const void *a, const void *b) {
    return strcmp(*(const char *const *)a, *(const char *const *)b);
}

static void u_inspect_to(USb *sb, UValue v) {
    switch (v.tag) {
    case U_NIL: sb_puts(sb, "nil"); break;
    case U_BOOL: sb_puts(sb, v.as.b ? "true" : "false"); break;
    case U_INT: sb_printf(sb, "%lld", (long long)v.as.i); break;
    case U_FLOAT: u_fmt_float(sb, v.as.f); break;
    case U_STR: sb_putn(sb, v.as.s->data, v.as.s->len); break;
    case U_ARR:
        sb_putc(sb, '[');
        for (size_t i = 0; i < v.as.a->len; i++) {
            if (i) sb_puts(sb, ", ");
            u_inspect_to(sb, v.as.a->items[i]);
        }
        sb_putc(sb, ']');
        break;
    case U_MAP: {
        UMap *m = v.as.m;
        USb *parts = u_xalloc((m->len ? m->len : 1) * sizeof(USb));
        for (size_t i = 0; i < m->len; i++) {
            u_inspect_to(&parts[i], m->entries[i].key);
            sb_puts(&parts[i], ": ");
            u_inspect_to(&parts[i], m->entries[i].value);
        }
        qsort(parts, m->len, sizeof(USb), u_cmp_sb);
        sb_putc(sb, '{');
        for (size_t i = 0; i < m->len; i++) {
            if (i) sb_puts(sb, ", ");
            sb_putn(sb, parts[i].buf, parts[i].len);
            free(parts[i].buf);
        }
        sb_putc(sb, '}');
        free(parts);
        break;
    }
    case U_FN: case U_BUILTIN: sb_puts(sb, v.as.fn->inspect); break;
    case U_MOD: {
        UMod *m = v.as.mod;
        const char **names = u_xalloc((size_t)(m->count ? m->count : 1) * sizeof(char *));
        memcpy(names, m->names, (size_t)m->count * sizeof(char *));
        qsort(names, (size_t)m->count, sizeof(char *), u_cmp_cstr);
        sb_printf(sb, "module %s {", m->name);
        for (int i = 0; i < m->count; i++) {
            if (i) sb_puts(sb, ", ");
            sb_puts(sb, names[i]);
        }
        sb_putc(sb, '}');
        free(names);
        break;
    }
    default: sb_puts(sb, "<undefined>"); break;
    }
}

/* ---------- maps ---------- */

static bool u_hashable(UValue v) {
    return v.tag == U_INT || v.tag == U_STR || v.tag == U_BOOL || v.tag == U_NIL;
}

static uint64_t u_hash(UValue v) {
    uint64_t h;
    switch (v.tag) {
    case U_STR: {
        h = 14695981039346656037ULL;
        for (size_t i = 0; i < v.as.s->len; i++) { h ^= (unsigned char)v.as.s->data[i]; h *= 1099511628211ULL; }
        break;
    }
    case U_INT: h = (uint64_t)v.as.i; break;
    case U_BOOL: h = v.as.b; break;
    default: h = 0; break;
    }
    h ^= (uint64_t)v.tag * 0x9E3779B97F4A7C15ULL;
    h ^= h >> 33; h *= 0xff51afd7ed558ccdULL; h ^= h >> 33;
    return h;
}

static bool u_key_eq(UValue a, UValue b) {
    if (a.tag != b.tag) return false;
    switch (a.tag) {
    case U_STR: return a.as.s->len == b.as.s->len && memcmp(a.as.s->data, b.as.s->data, a.as.s->len) == 0;
    case U_INT: return a.as.i == b.as.i;
    case U_BOOL: return a.as.b == b.as.b;
    case U_NIL: return true;
    default: return false;
    }
}

static UMap *u_map_new(void) { return u_alloc(sizeof(UMap)); }

static int64_t u_map_find(UMap *m, UValue key) {
    if (!m->icap) return -1;
    size_t mask = m->icap - 1;
    for (size_t i = u_hash(key) & mask;; i = (i + 1) & mask) {
        int64_t idx = m->index[i];
        if (idx < 0) return -1;
        if (u_key_eq(m->entries[idx].key, key)) return idx;
    }
}

static void u_map_reindex(UMap *m, size_t icap) {
    m->icap = icap;
    m->index = u_alloc(icap * sizeof(int64_t));
    for (size_t i = 0; i < icap; i++) m->index[i] = -1;
    size_t mask = icap - 1;
    for (size_t e = 0; e < m->len; e++) {
        size_t i = u_hash(m->entries[e].key) & mask;
        while (m->index[i] >= 0) i = (i + 1) & mask;
        m->index[i] = (int64_t)e;
    }
}

/* Inserting an existing key replaces both key and value, like the
 * interpreter's pairs[hash] = MapPair{key, value}. */
static void u_map_set(UMap *m, UValue key, UValue value) {
    int64_t idx = u_map_find(m, key);
    if (idx >= 0) { m->entries[idx].key = key; m->entries[idx].value = value; return; }
    if (m->len == m->cap) {
        m->cap = m->cap ? m->cap * 2 : 4;
        m->entries = u_realloc(m->entries, m->cap * sizeof(UEntry));
    }
    m->entries[m->len].key = key;
    m->entries[m->len].value = value;
    m->len++;
    if (m->len * 2 > m->icap) u_map_reindex(m, m->icap ? m->icap * 2 : 8);
    else {
        size_t mask = m->icap - 1;
        size_t i = u_hash(key) & mask;
        while (m->index[i] >= 0) i = (i + 1) & mask;
        m->index[i] = (int64_t)(m->len - 1);
    }
}

static UMap *u_map_copy(UMap *src) {
    UMap *m = u_map_new();
    for (size_t i = 0; i < src->len; i++) u_map_set(m, src->entries[i].key, src->entries[i].value);
    return m;
}

static void u_map_literal_key(UValue key) {
    if (!u_hashable(key)) u_panic("unusable as map key: %s", u_type_name(key));
}

/* ---------- equality (objectsEqual) ---------- */

static bool u_equal(UValue a, UValue b) {
    switch (a.tag) {
    case U_BOOL: return b.tag == U_BOOL && a.as.b == b.as.b;
    case U_NIL: return b.tag == U_NIL;
    case U_STR: return b.tag == U_STR && a.as.s->len == b.as.s->len && memcmp(a.as.s->data, b.as.s->data, a.as.s->len) == 0;
    case U_INT: return b.tag == U_INT && a.as.i == b.as.i;
    case U_FLOAT: return b.tag == U_FLOAT && a.as.f == b.as.f;
    case U_ARR:
        if (b.tag != U_ARR || a.as.a->len != b.as.a->len) return false;
        for (size_t i = 0; i < a.as.a->len; i++)
            if (!u_equal(a.as.a->items[i], b.as.a->items[i])) return false;
        return true;
    case U_MAP:
        if (b.tag != U_MAP || a.as.m->len != b.as.m->len) return false;
        for (size_t i = 0; i < a.as.m->len; i++) {
            int64_t j = u_map_find(b.as.m, a.as.m->entries[i].key);
            if (j < 0 || !u_equal(a.as.m->entries[i].value, b.as.m->entries[j].value)) return false;
        }
        return true;
    case U_FN: case U_BUILTIN: return b.tag == a.tag && a.as.fn == b.as.fn;
    case U_MOD: return b.tag == U_MOD && a.as.mod == b.as.mod;
    default: return false;
    }
}

/* ---------- operators ---------- */

enum { OP_ADD, OP_SUB, OP_MUL, OP_DIV, OP_MOD, OP_LT, OP_GT, OP_LE, OP_GE, OP_EQ, OP_NE, OP_AND, OP_OR };
static const char *u_op_names[] = {"+", "-", "*", "/", "%", "<", ">", "<=", ">=", "==", "!=", "&&", "||"};

static inline bool u_is_num(UValue v) { return v.tag == U_INT || v.tag == U_FLOAT; }
static inline double u_num(UValue v) { return v.tag == U_INT ? (double)v.as.i : v.as.f; }

static UValue u_binop(int op, UValue l, UValue r) {
    if (u_is_num(l) && u_is_num(r)) {
        if (l.tag == U_INT && r.tag == U_INT) {
            int64_t a = l.as.i, b = r.as.i;
            switch (op) {
            case OP_ADD: return uv_int((int64_t)((uint64_t)a + (uint64_t)b));
            case OP_SUB: return uv_int((int64_t)((uint64_t)a - (uint64_t)b));
            case OP_MUL: return uv_int((int64_t)((uint64_t)a * (uint64_t)b));
            case OP_DIV:
                if (b == 0) u_panic("division by zero");
                if (b == -1) return uv_int((int64_t)(0 - (uint64_t)a));
                return uv_int(a / b);
            case OP_MOD:
                if (b == 0) u_panic("modulo by zero");
                if (b == -1) return uv_int(0);
                return uv_int(a % b);
            case OP_LT: return uv_bool(a < b);
            case OP_GT: return uv_bool(a > b);
            case OP_LE: return uv_bool(a <= b);
            case OP_GE: return uv_bool(a >= b);
            case OP_EQ: return uv_bool(a == b);
            case OP_NE: return uv_bool(a != b);
            }
        }
        if (op == OP_MOD) u_panic("operator %% only supports integers");
        double a = u_num(l), b = u_num(r);
        switch (op) {
        case OP_ADD: return uv_float(a + b);
        case OP_SUB: return uv_float(a - b);
        case OP_MUL: return uv_float(a * b);
        case OP_DIV:
            if (b == 0) u_panic("division by zero");
            return uv_float(a / b);
        case OP_LT: return uv_bool(a < b);
        case OP_GT: return uv_bool(a > b);
        case OP_LE: return uv_bool(a <= b);
        case OP_GE: return uv_bool(a >= b);
        case OP_EQ: return uv_bool(fabs(a - b) == 0);
        case OP_NE: return uv_bool(fabs(a - b) != 0);
        }
        u_panic("unknown operator: %s %s %s", u_type_name(l), u_op_names[op], u_type_name(r));
    }
    if (op == OP_EQ) return uv_bool(u_equal(l, r));
    if (op == OP_NE) return uv_bool(!u_equal(l, r));
    if (l.tag == U_STR && r.tag == U_STR) {
        if (op == OP_ADD) {
            UStr *s = u_alloc(sizeof(UStr) + l.as.s->len + r.as.s->len + 1);
            s->len = l.as.s->len + r.as.s->len;
            memcpy(s->data, l.as.s->data, l.as.s->len);
            memcpy(s->data + l.as.s->len, r.as.s->data, r.as.s->len);
            s->data[s->len] = 0;
            return uv_strv(s);
        }
        u_panic("unknown operator: STRING %s STRING", u_op_names[op]);
    }
    if (op == OP_AND) return uv_bool(u_truthy(l) && u_truthy(r));
    if (op == OP_OR) return uv_bool(u_truthy(l) || u_truthy(r));
    if (l.tag != r.tag) u_panic("type mismatch: %s %s %s", u_type_name(l), u_op_names[op], u_type_name(r));
    u_panic("unknown operator: %s %s %s", u_type_name(l), u_op_names[op], u_type_name(r));
}

static UValue u_neg(UValue v) {
    if (v.tag == U_INT) return uv_int((int64_t)(0 - (uint64_t)v.as.i));
    if (v.tag == U_FLOAT) return uv_float(-v.as.f);
    u_panic("unknown operator: -%s", u_type_name(v));
}

static inline UValue u_not(UValue v) { return uv_bool(!u_truthy(v)); }

static UValue u_index(UValue l, UValue i) {
    if (l.tag == U_ARR && i.tag == U_INT) {
        if (i.as.i < 0 || (uint64_t)i.as.i >= l.as.a->len) return uv_nil();
        return l.as.a->items[i.as.i];
    }
    if (l.tag == U_MAP) {
        if (!u_hashable(i)) u_panic("unusable as map key: %s", u_type_name(i));
        int64_t idx = u_map_find(l.as.m, i);
        return idx < 0 ? uv_nil() : l.as.m->entries[idx].value;
    }
    if (l.tag == U_STR && i.tag == U_INT) {
        /* Strings index by character (code point), not byte. */
        UStr *s = l.as.s;
        if (i.as.i < 0) return uv_nil();
        int64_t n = 0;
        for (size_t pos = 0; pos < s->len; n++) {
            int size;
            int32_t r = u_utf8_decode((const unsigned char *)s->data + pos, s->len - pos, &size);
            if (n == i.as.i) {
                USb sb = {0};
                u_utf8_encode(&sb, r);
                return uv_sb_value(&sb);
            }
            pos += (size_t)size;
        }
        return uv_nil();
    }
    u_panic("index operator not supported: %s[%s]", u_type_name(l), u_type_name(i));
}

static size_t *u_sorted_entries(UMap *m);

/* Lists what a for loop binds; see evaluator.IterationItems. */
static void u_iter(UValue v, int vars, UArr **first, UArr **second) {
    UArr *a = u_arr_new(0), *b = vars == 2 ? u_arr_new(0) : NULL;
    switch (v.tag) {
    case U_ARR:
        for (size_t i = 0; i < v.as.a->len; i++) {
            if (vars == 1) u_arr_push(a, v.as.a->items[i]);
            else { u_arr_push(a, uv_int((int64_t)i)); u_arr_push(b, v.as.a->items[i]); }
        }
        break;
    case U_MAP: {
        size_t *idx = u_sorted_entries(v.as.m);
        for (size_t i = 0; i < v.as.m->len; i++) {
            u_arr_push(a, v.as.m->entries[idx[i]].key);
            if (b) u_arr_push(b, v.as.m->entries[idx[i]].value);
        }
        free(idx);
        break;
    }
    case U_STR: {
        UStr *s = v.as.s;
        int64_t n = 0;
        for (size_t i = 0; i < s->len; n++) {
            int size;
            int32_t r = u_utf8_decode((const unsigned char *)s->data + i, s->len - i, &size);
            USb sb = {0};
            u_utf8_encode(&sb, r);
            UValue ch = uv_sb_value(&sb);
            if (vars == 1) u_arr_push(a, ch);
            else { u_arr_push(a, uv_int(n)); u_arr_push(b, ch); }
            i += (size_t)size;
        }
        break;
    }
    default:
        u_panic("cannot iterate over %s", u_type_name(v));
    }
    *first = a;
    *second = b;
}

static UValue u_member(UValue obj, const char *name) {
    if (obj.tag == U_MAP) {
        int64_t idx = u_map_find(obj.as.m, uv_cstr(name));
        return idx < 0 ? uv_nil() : obj.as.m->entries[idx].value;
    }
    if (obj.tag != U_MOD) u_panic("member access expects module or map, got %s", u_type_name(obj));
    UMod *m = obj.as.mod;
    for (int i = 0; i < m->count; i++)
        if (strcmp(m->names[i], name) == 0) return m->values[i];
    u_panic("module \"%s\" has no member \"%s\"", m->name, name);
}

static UValue u_call(UValue f, int argc, UValue *argv) {
    if (f.tag == U_BUILTIN) return f.as.fn->code(NULL, argc, argv);
    if (f.tag != U_FN) u_panic("not a function: %s", u_type_name(f));
    if (argc != f.as.fn->arity) u_panic("wrong number of arguments: expected %d, got %d", f.as.fn->arity, argc);
    if (u_depth >= U_MAX_DEPTH) u_panic("maximum call depth exceeded");
    u_depth++;
    UValue result = f.as.fn->code(f.as.fn->env, argc, argv);
    u_depth--;
    return result;
}

/* ---------- builtin helpers ---------- */

#define U_ARGC(name, n) do { if (argc != (n)) u_panic("%s expects %d %s, got %d", name, n, (n) == 1 ? "argument" : "arguments", argc); } while (0)
#define U_BUILTIN_FN(fn) static UValue fn(UFrame *env_, int argc, UValue *argv)

static UStr *u_want_str(UValue v, const char *what) {
    if (v.tag != U_STR) u_panic("%s must be STRING, got %s", what, u_type_name(v));
    return v.as.s;
}

static void u_go_quote(USb *sb, const char *s, size_t n) {
    sb_putc(sb, '"');
    for (size_t i = 0; i < n; i++) {
        unsigned char c = (unsigned char)s[i];
        switch (c) {
        case '"': sb_puts(sb, "\\\""); break;
        case '\\': sb_puts(sb, "\\\\"); break;
        case '\n': sb_puts(sb, "\\n"); break;
        case '\t': sb_puts(sb, "\\t"); break;
        case '\r': sb_puts(sb, "\\r"); break;
        default:
            if (c < 0x20 || c == 0x7f) sb_printf(sb, "\\x%02x", c);
            else sb_putc(sb, (char)c);
        }
    }
    sb_putc(sb, '"');
}

/* Go's syscall error strings are glibc's with a lowercase first letter. */
static void u_errno_text(USb *sb, int err) {
    const char *msg = strerror(err);
    if (msg[0] >= 'A' && msg[0] <= 'Z') { sb_putc(sb, (char)(msg[0] + 32)); sb_puts(sb, msg + 1); }
    else sb_puts(sb, msg);
}

static void u_file_error(const char *builtin, const char *op, UStr *path, int err) {
    USb sb = {0};
    sb_printf(&sb, "%s failed for ", builtin);
    u_go_quote(&sb, path->data, path->len);
    sb_printf(&sb, ": %s %s: ", op, path->data);
    u_errno_text(&sb, err);
    u_panic("%s", sb.buf);
}

/* ---------- global builtins ---------- */

static void u_join_args(USb *sb, int argc, UValue *argv) {
    for (int i = 0; i < argc; i++) {
        if (i) sb_putc(sb, ' ');
        u_inspect_to(sb, argv[i]);
    }
}

U_BUILTIN_FN(ub_print) {
    (void)env_;
    USb sb = {0};
    u_join_args(&sb, argc, argv);
    if (sb.len) fwrite(sb.buf, 1, sb.len, stdout);
    free(sb.buf);
    return uv_nil();
}

U_BUILTIN_FN(ub_println) {
    (void)env_;
    USb sb = {0};
    u_join_args(&sb, argc, argv);
    sb_putc(&sb, '\n');
    fwrite(sb.buf, 1, sb.len, stdout);
    free(sb.buf);
    return uv_nil();
}

U_BUILTIN_FN(ub_len) {
    (void)env_;
    U_ARGC("len", 1);
    switch (argv[0].tag) {
    case U_STR: {
        UStr *s = argv[0].as.s;
        int64_t n = 0;
        for (size_t pos = 0; pos < s->len; n++) {
            unsigned char c = (unsigned char)s->data[pos];
            if (c < 0x80) { pos++; continue; }
            int size;
            u_utf8_decode((const unsigned char *)s->data + pos, s->len - pos, &size);
            pos += (size_t)size;
        }
        return uv_int(n);
    }
    case U_ARR: return uv_int((int64_t)argv[0].as.a->len);
    case U_MAP: return uv_int((int64_t)argv[0].as.m->len);
    default: u_panic("len does not support %s", u_type_name(argv[0]));
    }
}

U_BUILTIN_FN(ub_type) {
    (void)env_;
    U_ARGC("type", 1);
    const char *name = u_type_name(argv[0]);
    char lower[32];
    size_t n = strlen(name);
    for (size_t i = 0; i <= n; i++) lower[i] = (char)(name[i] >= 'A' && name[i] <= 'Z' ? name[i] + 32 : name[i]);
    return uv_cstr(lower);
}

U_BUILTIN_FN(ub_read_file) {
    (void)env_;
    U_ARGC("read_file", 1);
    UStr *path = u_want_str(argv[0], "read_file path");
    FILE *f = fopen(path->data, "rb");
    if (!f) u_file_error("read_file", "open", path, errno);
    USb sb = {0};
    char buf[8192];
    size_t n;
    while ((n = fread(buf, 1, sizeof buf, f)) > 0) sb_putn(&sb, buf, n);
    if (ferror(f)) { int err = errno; fclose(f); u_file_error("read_file", "read", path, err); }
    fclose(f);
    return uv_sb_value(&sb);
}

static UValue u_write_common(const char *name, int argc, UValue *argv, const char *mode) {
    U_ARGC(name, 2);
    char what[64];
    snprintf(what, sizeof what, "%s path", name);
    UStr *path = u_want_str(argv[0], what);
    snprintf(what, sizeof what, "%s content", name);
    UStr *content = u_want_str(argv[1], what);
    FILE *f = fopen(path->data, mode);
    if (!f) u_file_error(name, "open", path, errno);
    if (content->len && fwrite(content->data, 1, content->len, f) != content->len) {
        int err = errno; fclose(f); u_file_error(name, "write", path, err);
    }
    fclose(f);
    return uv_int((int64_t)content->len);
}

U_BUILTIN_FN(ub_write_file) { (void)env_; return u_write_common("write_file", argc, argv, "wb"); }
U_BUILTIN_FN(ub_append_file) { (void)env_; return u_write_common("append_file", argc, argv, "ab"); }

/* ---------- strings ---------- */

static bool u_has_prefix(UStr *s, UStr *p) { return p->len <= s->len && memcmp(s->data, p->data, p->len) == 0; }
static bool u_has_suffix(UStr *s, UStr *p) { return p->len <= s->len && memcmp(s->data + s->len - p->len, p->data, p->len) == 0; }

static const char *u_find(const char *hay, size_t hn, const char *needle, size_t nn) {
    if (nn == 0) return hay;
    if (nn > hn) return NULL;
    return memmem(hay, hn, needle, nn);
}

U_BUILTIN_FN(us_contains) {
    (void)env_;
    U_ARGC("strings.contains", 2);
    UStr *a = u_want_str(argv[0], "strings.contains input");
    UStr *b = u_want_str(argv[1], "strings.contains substr");
    return uv_bool(u_find(a->data, a->len, b->data, b->len) != NULL);
}

U_BUILTIN_FN(us_has_prefix) {
    (void)env_;
    U_ARGC("strings.has_prefix", 2);
    UStr *a = u_want_str(argv[0], "strings.has_prefix input");
    UStr *b = u_want_str(argv[1], "strings.has_prefix prefix");
    return uv_bool(u_has_prefix(a, b));
}

U_BUILTIN_FN(us_has_suffix) {
    (void)env_;
    U_ARGC("strings.has_suffix", 2);
    UStr *a = u_want_str(argv[0], "strings.has_suffix input");
    UStr *b = u_want_str(argv[1], "strings.has_suffix suffix");
    return uv_bool(u_has_suffix(a, b));
}

static UValue u_strings_join(int argc, UValue *argv) {
    U_ARGC("strings.join", 2);
    if (argv[0].tag != U_ARR) u_panic("strings.join first argument must be ARRAY, got %s", u_type_name(argv[0]));
    UStr *sep = u_want_str(argv[1], "strings.join separator");
    UArr *a = argv[0].as.a;
    USb sb = {0};
    for (size_t i = 0; i < a->len; i++) {
        if (a->items[i].tag != U_STR) { free(sb.buf); u_panic("strings.join array elements must be STRING, got %s", u_type_name(a->items[i])); }
        if (i) sb_putn(&sb, sep->data, sep->len);
        sb_putn(&sb, a->items[i].as.s->data, a->items[i].as.s->len);
    }
    return uv_sb_value(&sb);
}

U_BUILTIN_FN(us_join) { (void)env_; return u_strings_join(argc, argv); }

/* Simple case mapping for ASCII, Latin-1, Latin Extended-A, Greek and
 * Cyrillic, covering what unicode.ToUpper/ToLower do for those blocks. */
static int32_t u_to_upper(int32_t r) {
    if (r >= 'a' && r <= 'z') return r - 32;
    if (r < 0x80) return r;
    if (r == 0xB5) return 0x39C;
    if ((r >= 0xE0 && r <= 0xFE && r != 0xF7)) return r - 32;
    if (r == 0xFF) return 0x178;
    if (r == 0x131) return 'I';
    if (r == 0x17F) return 'S';
    if ((r >= 0x100 && r <= 0x12F) || (r >= 0x132 && r <= 0x137) || (r >= 0x14A && r <= 0x177)) return (r & 1) ? r - 1 : r;
    if ((r >= 0x139 && r <= 0x148) || (r >= 0x179 && r <= 0x17E)) return (r & 1) ? r : r - 1;
    if (r == 0x3AC) return 0x386;
    if (r >= 0x3AD && r <= 0x3AF) return r - 37;
    if (r == 0x3C2) return 0x3A3;
    if (r >= 0x3B1 && r <= 0x3CB) return r - 32;
    if (r == 0x3CC) return 0x38C;
    if (r == 0x3CD || r == 0x3CE) return r - 63;
    if (r >= 0x430 && r <= 0x44F) return r - 32;
    if (r >= 0x450 && r <= 0x45F) return r - 80;
    if ((r >= 0x460 && r <= 0x481) || (r >= 0x48A && r <= 0x4BF)) return (r & 1) ? r - 1 : r;
    return r;
}

static int32_t u_to_lower(int32_t r) {
    if (r >= 'A' && r <= 'Z') return r + 32;
    if (r < 0x80) return r;
    if ((r >= 0xC0 && r <= 0xDE && r != 0xD7)) return r + 32;
    if (r == 0x130) return 'i';
    if (r == 0x178) return 0xFF;
    if ((r >= 0x100 && r <= 0x12F) || (r >= 0x132 && r <= 0x137) || (r >= 0x14A && r <= 0x177)) return (r & 1) ? r : r + 1;
    if ((r >= 0x139 && r <= 0x148) || (r >= 0x179 && r <= 0x17E)) return (r & 1) ? r + 1 : r;
    if (r == 0x386) return 0x3AC;
    if (r >= 0x388 && r <= 0x38A) return r + 37;
    if (r == 0x38C) return 0x3CC;
    if (r == 0x38E || r == 0x38F) return r + 63;
    if ((r >= 0x391 && r <= 0x3A1) || (r >= 0x3A3 && r <= 0x3AB)) return r + 32;
    if (r >= 0x410 && r <= 0x42F) return r + 32;
    if (r >= 0x400 && r <= 0x40F) return r + 80;
    if ((r >= 0x460 && r <= 0x481) || (r >= 0x48A && r <= 0x4BF)) return (r & 1) ? r : r + 1;
    return r;
}

static UValue u_map_case(UStr *s, int32_t (*fn)(int32_t)) {
    bool ascii = true;
    for (size_t i = 0; i < s->len; i++) if ((unsigned char)s->data[i] >= 0x80) { ascii = false; break; }
    USb sb = {0};
    if (ascii) {
        sb_grow(&sb, s->len);
        for (size_t i = 0; i < s->len; i++) sb_putc(&sb, (char)fn((unsigned char)s->data[i]));
        return uv_sb_value(&sb);
    }
    for (size_t i = 0; i < s->len;) {
        int size;
        int32_t r = u_utf8_decode((const unsigned char *)s->data + i, s->len - i, &size);
        u_utf8_encode(&sb, fn(r));
        i += (size_t)size;
    }
    return uv_sb_value(&sb);
}

U_BUILTIN_FN(us_upper) {
    (void)env_;
    U_ARGC("strings.upper", 1);
    return u_map_case(u_want_str(argv[0], "strings.upper input"), u_to_upper);
}

U_BUILTIN_FN(us_lower) {
    (void)env_;
    U_ARGC("strings.lower", 1);
    return u_map_case(u_want_str(argv[0], "strings.lower input"), u_to_lower);
}

static bool u_is_space(int32_t r) {
    switch (r) {
    case '\t': case '\n': case '\v': case '\f': case '\r': case ' ': case 0x85: case 0xA0:
    case 0x1680: case 0x2028: case 0x2029: case 0x202F: case 0x205F: case 0x3000:
        return true;
    }
    return r >= 0x2000 && r <= 0x200A;
}

U_BUILTIN_FN(us_trim) {
    (void)env_;
    U_ARGC("strings.trim", 1);
    UStr *s = u_want_str(argv[0], "strings.trim input");
    size_t start = 0, end = 0, i = 0;
    bool seen = false;
    while (i < s->len) {
        int size;
        int32_t r = u_utf8_decode((const unsigned char *)s->data + i, s->len - i, &size);
        bool invalid = (r == 0xFFFD && size == 1);
        if (invalid || !u_is_space(r)) {
            if (!seen) { start = i; seen = true; }
            end = i + (size_t)size;
        }
        i += (size_t)size;
    }
    if (!seen) return uv_str_n("", 0);
    return uv_str_n(s->data + start, end - start);
}

U_BUILTIN_FN(us_replace) {
    (void)env_;
    U_ARGC("strings.replace", 3);
    UStr *s = u_want_str(argv[0], "strings.replace input");
    UStr *old = u_want_str(argv[1], "strings.replace old");
    UStr *repl = u_want_str(argv[2], "strings.replace new");
    USb sb = {0};
    if (old->len == 0) {
        /* Go inserts the replacement before every rune and at the end. */
        size_t i = 0;
        while (i < s->len) {
            int size;
            sb_putn(&sb, repl->data, repl->len);
            u_utf8_decode((const unsigned char *)s->data + i, s->len - i, &size);
            sb_putn(&sb, s->data + i, (size_t)size);
            i += (size_t)size;
        }
        sb_putn(&sb, repl->data, repl->len);
        return uv_sb_value(&sb);
    }
    const char *p = s->data, *end = s->data + s->len;
    for (;;) {
        const char *hit = u_find(p, (size_t)(end - p), old->data, old->len);
        if (!hit) break;
        sb_putn(&sb, p, (size_t)(hit - p));
        sb_putn(&sb, repl->data, repl->len);
        p = hit + old->len;
    }
    sb_putn(&sb, p, (size_t)(end - p));
    return uv_sb_value(&sb);
}

U_BUILTIN_FN(us_split) {
    (void)env_;
    U_ARGC("strings.split", 2);
    UStr *s = u_want_str(argv[0], "strings.split input");
    UStr *sep = u_want_str(argv[1], "strings.split separator");
    UArr *out = u_arr_new(0);
    if (sep->len == 0) {
        for (size_t i = 0; i < s->len;) {
            int size;
            u_utf8_decode((const unsigned char *)s->data + i, s->len - i, &size);
            u_arr_push(out, uv_str_n(s->data + i, (size_t)size));
            i += (size_t)size;
        }
        return uv_arrv(out);
    }
    const char *p = s->data, *end = s->data + s->len;
    for (;;) {
        const char *hit = u_find(p, (size_t)(end - p), sep->data, sep->len);
        if (!hit) break;
        u_arr_push(out, uv_str_n(p, (size_t)(hit - p)));
        p = hit + sep->len;
    }
    u_arr_push(out, uv_str_n(p, (size_t)(end - p)));
    return uv_arrv(out);
}

/* ---------- arrays ---------- */

static UArr *u_want_arr(UValue v, const char *fmt) {
    if (v.tag != U_ARR) u_panic(fmt, u_type_name(v));
    return v.as.a;
}

static UArr *u_arr_copy(UArr *src, size_t extra) {
    UArr *a = u_arr_new(src->len + extra);
    if (src->len) memcpy(a->items, src->items, src->len * sizeof(UValue));
    a->len = src->len;
    return a;
}

U_BUILTIN_FN(ua_append) {
    (void)env_;
    U_ARGC("arrays.append", 2);
    UArr *a = u_arr_copy(u_want_arr(argv[0], "arrays.append first argument must be ARRAY, got %s"), 1);
    u_arr_push(a, argv[1]);
    return uv_arrv(a);
}

U_BUILTIN_FN(ua_first) {
    (void)env_;
    U_ARGC("arrays.first", 1);
    UArr *a = u_want_arr(argv[0], "arrays.first expects ARRAY, got %s");
    return a->len ? a->items[0] : uv_nil();
}

U_BUILTIN_FN(ua_last) {
    (void)env_;
    U_ARGC("arrays.last", 1);
    UArr *a = u_want_arr(argv[0], "arrays.last expects ARRAY, got %s");
    return a->len ? a->items[a->len - 1] : uv_nil();
}

U_BUILTIN_FN(ua_join) { (void)env_; return u_strings_join(argc, argv); }

U_BUILTIN_FN(ua_range) {
    (void)env_;
    U_ARGC("arrays.range", 2);
    if (argv[0].tag != U_INT) u_panic("arrays.range start must be INTEGER, got %s", u_type_name(argv[0]));
    if (argv[1].tag != U_INT) u_panic("arrays.range end must be INTEGER, got %s", u_type_name(argv[1]));
    int64_t start = argv[0].as.i, end = argv[1].as.i;
    UArr *a = u_arr_new(0);
    if (start <= end) for (int64_t i = start; i < end; i++) u_arr_push(a, uv_int(i));
    else for (int64_t i = start; i > end; i--) u_arr_push(a, uv_int(i));
    return uv_arrv(a);
}

U_BUILTIN_FN(ua_reverse) {
    (void)env_;
    U_ARGC("arrays.reverse", 1);
    UArr *src = u_want_arr(argv[0], "arrays.reverse expects ARRAY, got %s");
    UArr *a = u_arr_new(src->len);
    for (size_t i = src->len; i > 0; i--) a->items[a->len++] = src->items[i - 1];
    return uv_arrv(a);
}

U_BUILTIN_FN(ua_slice) {
    (void)env_;
    U_ARGC("arrays.slice", 3);
    UArr *src = u_want_arr(argv[0], "arrays.slice first argument must be ARRAY, got %s");
    if (argv[1].tag != U_INT) u_panic("arrays.slice start must be INTEGER, got %s", u_type_name(argv[1]));
    if (argv[2].tag != U_INT) u_panic("arrays.slice end must be INTEGER, got %s", u_type_name(argv[2]));
    int64_t start = argv[1].as.i, end = argv[2].as.i;
    if (start < 0 || end < start || end > (int64_t)src->len)
        u_panic("arrays.slice bounds out of range: %lld..%lld for length %zu", (long long)start, (long long)end, src->len);
    UArr *a = u_arr_new((size_t)(end - start));
    for (int64_t i = start; i < end; i++) a->items[a->len++] = src->items[i];
    return uv_arrv(a);
}

U_BUILTIN_FN(ua_map) {
    (void)env_;
    U_ARGC("arrays.map", 2);
    UArr *src = u_want_arr(argv[0], "arrays.map first argument must be ARRAY, got %s");
    UValue fn = argv[1];
    UArr *a = u_arr_new(src->len);
    for (size_t i = 0; i < src->len; i++) {
        UValue arg = src->items[i];
        a->items[a->len++] = u_call(fn, 1, &arg);
    }
    return uv_arrv(a);
}

U_BUILTIN_FN(ua_filter) {
    (void)env_;
    U_ARGC("arrays.filter", 2);
    UArr *src = u_want_arr(argv[0], "arrays.filter first argument must be ARRAY, got %s");
    UValue fn = argv[1];
    UArr *a = u_arr_new(0);
    for (size_t i = 0; i < src->len; i++) {
        UValue arg = src->items[i];
        if (u_truthy(u_call(fn, 1, &arg))) u_arr_push(a, arg);
    }
    return uv_arrv(a);
}

U_BUILTIN_FN(ua_reduce) {
    (void)env_;
    U_ARGC("arrays.reduce", 3);
    UArr *src = u_want_arr(argv[0], "arrays.reduce first argument must be ARRAY, got %s");
    UValue acc = argv[1], fn = argv[2];
    for (size_t i = 0; i < src->len; i++) {
        UValue args[2] = {acc, src->items[i]};
        acc = u_call(fn, 2, args);
    }
    return acc;
}

/* ---------- maps module ---------- */

static UMap *u_want_map(UValue v, const char *fmt) {
    if (v.tag != U_MAP) u_panic(fmt, u_type_name(v));
    return v.as.m;
}

U_BUILTIN_FN(um_get) {
    (void)env_;
    if (argc != 2 && argc != 3) u_panic("maps.get expects 2 or 3 arguments, got %d", argc);
    UMap *m = u_want_map(argv[0], "maps.get first argument must be MAP, got %s");
    if (!u_hashable(argv[1])) u_panic("maps.get key is not hashable: %s", u_type_name(argv[1]));
    int64_t idx = u_map_find(m, argv[1]);
    if (idx >= 0) return m->entries[idx].value;
    return argc == 3 ? argv[2] : uv_nil();
}

U_BUILTIN_FN(um_has) {
    (void)env_;
    U_ARGC("maps.has", 2);
    UMap *m = u_want_map(argv[0], "maps.has first argument must be MAP, got %s");
    if (!u_hashable(argv[1])) u_panic("maps.has key is not hashable: %s", u_type_name(argv[1]));
    return uv_bool(u_map_find(m, argv[1]) >= 0);
}

U_BUILTIN_FN(um_set) {
    (void)env_;
    U_ARGC("maps.set", 3);
    UMap *m = u_want_map(argv[0], "maps.set first argument must be MAP, got %s");
    if (!u_hashable(argv[1])) u_panic("maps.set key is not hashable: %s", u_type_name(argv[1]));
    UMap *out = u_map_copy(m);
    u_map_set(out, argv[1], argv[2]);
    return uv_mapv(out);
}

U_BUILTIN_FN(um_merge) {
    (void)env_;
    U_ARGC("maps.merge", 2);
    UMap *l = u_want_map(argv[0], "maps.merge first argument must be MAP, got %s");
    UMap *r = u_want_map(argv[1], "maps.merge second argument must be MAP, got %s");
    UMap *out = u_map_copy(l);
    for (size_t i = 0; i < r->len; i++) u_map_set(out, r->entries[i].key, r->entries[i].value);
    return uv_mapv(out);
}

typedef struct { USb text; size_t idx; } UKeyOrder;

static int u_cmp_keyorder(const void *a, const void *b) {
    return u_cmp_sb(&((const UKeyOrder *)a)->text, &((const UKeyOrder *)b)->text);
}

/* Entry indexes sorted by the key's Inspect text, like maps.keys/values. */
static size_t *u_sorted_entries(UMap *m) {
    UKeyOrder *order = u_xalloc((m->len ? m->len : 1) * sizeof(UKeyOrder));
    for (size_t i = 0; i < m->len; i++) { u_inspect_to(&order[i].text, m->entries[i].key); order[i].idx = i; }
    qsort(order, m->len, sizeof(UKeyOrder), u_cmp_keyorder);
    size_t *idx = u_xalloc((m->len ? m->len : 1) * sizeof(size_t));
    for (size_t i = 0; i < m->len; i++) { idx[i] = order[i].idx; free(order[i].text.buf); }
    free(order);
    return idx;
}

U_BUILTIN_FN(um_keys) {
    (void)env_;
    U_ARGC("maps.keys", 1);
    UMap *m = u_want_map(argv[0], "maps.keys expects MAP, got %s");
    size_t *idx = u_sorted_entries(m);
    UArr *a = u_arr_new(m->len);
    for (size_t i = 0; i < m->len; i++) a->items[a->len++] = m->entries[idx[i]].key;
    free(idx);
    return uv_arrv(a);
}

U_BUILTIN_FN(um_values) {
    (void)env_;
    U_ARGC("maps.values", 1);
    UMap *m = u_want_map(argv[0], "maps.values expects MAP, got %s");
    size_t *idx = u_sorted_entries(m);
    UArr *a = u_arr_new(m->len);
    for (size_t i = 0; i < m->len; i++) a->items[a->len++] = m->entries[idx[i]].value;
    free(idx);
    return uv_arrv(a);
}

/* ---------- json ---------- */

static int u_cmp_entry_key(const void *a, const void *b) {
    const UEntry *x = *(UEntry *const *)a, *y = *(UEntry *const *)b;
    USb sx = {x->key.as.s->data, x->key.as.s->len, 0}, sy = {y->key.as.s->data, y->key.as.s->len, 0};
    return u_cmp_sb(&sx, &sy);
}

static void u_json_string(USb *sb, const char *s, size_t n) {
    static const char hex[] = "0123456789abcdef";
    sb_putc(sb, '"');
    for (size_t i = 0; i < n;) {
        unsigned char c = (unsigned char)s[i];
        if (c < 0x80) {
            switch (c) {
            case '\\': sb_puts(sb, "\\\\"); break;
            case '"': sb_puts(sb, "\\\""); break;
            case '\b': sb_puts(sb, "\\b"); break;
            case '\f': sb_puts(sb, "\\f"); break;
            case '\n': sb_puts(sb, "\\n"); break;
            case '\r': sb_puts(sb, "\\r"); break;
            case '\t': sb_puts(sb, "\\t"); break;
            default:
                if (c < 0x20 || c == '<' || c == '>' || c == '&') {
                    char esc[7] = {'\\', 'u', '0', '0', hex[c >> 4], hex[c & 0xF], 0};
                    sb_puts(sb, esc);
                } else {
                    sb_putc(sb, (char)c);
                }
            }
            i++;
            continue;
        }
        int size;
        int32_t r = u_utf8_decode((const unsigned char *)s + i, n - i, &size);
        if (r == 0xFFFD && size == 1) sb_puts(sb, "\\ufffd");
        else if (r == 0x2028) sb_puts(sb, "\\u2028");
        else if (r == 0x2029) sb_puts(sb, "\\u2029");
        else sb_putn(sb, s + i, (size_t)size);
        i += (size_t)size;
    }
    sb_putc(sb, '"');
}

/* Matches encoding/json's float encoder. */
static bool u_json_float(USb *sb, double v, USb *err) {
    if (isnan(v) || isinf(v)) {
        USb tmp = {0};
        u_fmt_float(&tmp, v);
        sb_printf(err, "json: unsupported value: %s", tmp.buf);
        free(tmp.buf);
        return false;
    }
    double a = fabs(v);
    if (signbit(v)) sb_putc(sb, '-');
    char d[40]; int nd, dp;
    u_shortest(a, d, &nd, &dp);
    if (a != 0 && (a < 1e-6 || a >= 1e21)) {
        USb e = {0};
        u_fmt_e(&e, d, nd, dp);
        /* clean up e-09 to e-9 */
        size_t n = e.len;
        if (n >= 4 && e.buf[n - 4] == 'e' && e.buf[n - 3] == '-' && e.buf[n - 2] == '0') {
            e.buf[n - 2] = e.buf[n - 1];
            e.len--;
        }
        sb_putn(sb, e.buf, e.len);
        free(e.buf);
    } else {
        u_fmt_f(sb, d, nd, dp);
    }
    return true;
}

static bool u_json_encode(USb *sb, UValue v, USb *err) {
    switch (v.tag) {
    case U_NIL: sb_puts(sb, "null"); return true;
    case U_BOOL: sb_puts(sb, v.as.b ? "true" : "false"); return true;
    case U_INT: sb_printf(sb, "%lld", (long long)v.as.i); return true;
    case U_FLOAT: return u_json_float(sb, v.as.f, err);
    case U_STR: u_json_string(sb, v.as.s->data, v.as.s->len); return true;
    case U_ARR:
        sb_putc(sb, '[');
        for (size_t i = 0; i < v.as.a->len; i++) {
            if (i) sb_putc(sb, ',');
            if (!u_json_encode(sb, v.as.a->items[i], err)) return false;
        }
        sb_putc(sb, ']');
        return true;
    case U_MAP: {
        UMap *m = v.as.m;
        UEntry **entries = u_xalloc((m->len ? m->len : 1) * sizeof(UEntry *));
        for (size_t i = 0; i < m->len; i++) {
            if (m->entries[i].key.tag != U_STR) {
                sb_printf(err, "JSON object keys must be STRING, got %s", u_type_name(m->entries[i].key));
                free(entries);
                return false;
            }
            entries[i] = &m->entries[i];
        }
        USb body = {0};
        for (size_t i = 0; i < m->len; i++)
            if (!u_json_encode(&body, m->entries[i].value, err)) { free(entries); free(body.buf); return false; }
        free(body.buf);
        qsort(entries, m->len, sizeof(UEntry *), u_cmp_entry_key);
        sb_putc(sb, '{');
        for (size_t i = 0; i < m->len; i++) {
            if (i) sb_putc(sb, ',');
            u_json_string(sb, entries[i]->key.as.s->data, entries[i]->key.as.s->len);
            sb_putc(sb, ':');
            u_json_encode(sb, entries[i]->value, err);
        }
        sb_putc(sb, '}');
        free(entries);
        return true;
    }
    default:
        sb_printf(err, "cannot encode %s", u_type_name(v));
        return false;
    }
}

U_BUILTIN_FN(uj_stringify) {
    (void)env_;
    U_ARGC("json.stringify", 1);
    USb sb = {0}, err = {0};
    if (!u_json_encode(&sb, argv[0], &err)) u_panic("json.stringify failed: %s", err.buf);
    return uv_sb_value(&sb);
}

typedef struct { const char *s; size_t n, i; char err[160]; } UJson;

static void uj_skip(UJson *j) {
    while (j->i < j->n && (j->s[j->i] == ' ' || j->s[j->i] == '\t' || j->s[j->i] == '\n' || j->s[j->i] == '\r')) j->i++;
}

static bool uj_fail(UJson *j, const char *context) {
    if (j->err[0]) return false;
    if (j->i >= j->n) snprintf(j->err, sizeof j->err, "unexpected end of JSON input");
    else {
        char c = j->s[j->i];
        if (c == '\'' ) snprintf(j->err, sizeof j->err, "invalid character '\\'' %s", context);
        else if ((unsigned char)c < 0x20 || (unsigned char)c >= 0x80) snprintf(j->err, sizeof j->err, "invalid character '\\x%02x' %s", (unsigned char)c, context);
        else snprintf(j->err, sizeof j->err, "invalid character '%c' %s", c, context);
    }
    return false;
}

static bool uj_value(UJson *j, UValue *out);

static int uj_hex(char c) {
    if (c >= '0' && c <= '9') return c - '0';
    if (c >= 'a' && c <= 'f') return c - 'a' + 10;
    if (c >= 'A' && c <= 'F') return c - 'A' + 10;
    return -1;
}

static bool uj_u4(UJson *j, int32_t *r) {
    if (j->i + 4 > j->n) { j->i = j->n; return uj_fail(j, ""); }
    int32_t v = 0;
    for (int k = 0; k < 4; k++) {
        int h = uj_hex(j->s[j->i + k]);
        if (h < 0) { j->i += k; return uj_fail(j, "in \\u hexadecimal character escape"); }
        v = v * 16 + h;
    }
    j->i += 4;
    *r = v;
    return true;
}

static bool uj_string(UJson *j, UValue *out) {
    j->i++; /* opening quote */
    USb sb = {0};
    while (j->i < j->n) {
        unsigned char c = (unsigned char)j->s[j->i];
        if (c == '"') { j->i++; *out = uv_sb_value(&sb); return true; }
        if (c < 0x20) { free(sb.buf); return uj_fail(j, "in string literal"); }
        if (c == '\\') {
            j->i++;
            if (j->i >= j->n) break;
            char e = j->s[j->i++];
            switch (e) {
            case '"': sb_putc(&sb, '"'); break;
            case '\\': sb_putc(&sb, '\\'); break;
            case '/': sb_putc(&sb, '/'); break;
            case 'b': sb_putc(&sb, '\b'); break;
            case 'f': sb_putc(&sb, '\f'); break;
            case 'n': sb_putc(&sb, '\n'); break;
            case 'r': sb_putc(&sb, '\r'); break;
            case 't': sb_putc(&sb, '\t'); break;
            case 'u': {
                int32_t r;
                if (!uj_u4(j, &r)) { free(sb.buf); return false; }
                if (r >= 0xD800 && r < 0xDC00 && j->i + 6 <= j->n && j->s[j->i] == '\\' && j->s[j->i + 1] == 'u') {
                    size_t save = j->i;
                    j->i += 2;
                    int32_t lo;
                    if (uj_u4(j, &lo) && lo >= 0xDC00 && lo < 0xE000) r = 0x10000 + ((r - 0xD800) << 10) + (lo - 0xDC00);
                    else { j->i = save; j->err[0] = 0; r = 0xFFFD; }
                } else if (r >= 0xD800 && r < 0xE000) {
                    r = 0xFFFD;
                }
                u_utf8_encode(&sb, r);
                break;
            }
            default:
                j->i--;
                free(sb.buf);
                return uj_fail(j, "in string escape code");
            }
            continue;
        }
        if (c >= 0x80) {
            int size;
            int32_t r = u_utf8_decode((const unsigned char *)j->s + j->i, j->n - j->i, &size);
            if (r == 0xFFFD && size == 1) sb_puts(&sb, "\xEF\xBF\xBD");
            else sb_putn(&sb, j->s + j->i, (size_t)size);
            j->i += (size_t)size;
            continue;
        }
        sb_putc(&sb, (char)c);
        j->i++;
    }
    free(sb.buf);
    j->i = j->n;
    return uj_fail(j, "");
}

static bool uj_number(UJson *j, UValue *out) {
    size_t start = j->i;
    if (j->s[j->i] == '-') j->i++;
    if (j->i >= j->n) return uj_fail(j, "in numeric literal");
    if (j->s[j->i] == '0') j->i++;
    else if (j->s[j->i] >= '1' && j->s[j->i] <= '9') while (j->i < j->n && j->s[j->i] >= '0' && j->s[j->i] <= '9') j->i++;
    else return uj_fail(j, "in numeric literal");
    if (j->i < j->n && j->s[j->i] == '.') {
        j->i++;
        if (j->i >= j->n || j->s[j->i] < '0' || j->s[j->i] > '9') return uj_fail(j, "after decimal point in numeric literal");
        while (j->i < j->n && j->s[j->i] >= '0' && j->s[j->i] <= '9') j->i++;
    }
    if (j->i < j->n && (j->s[j->i] == 'e' || j->s[j->i] == 'E')) {
        j->i++;
        if (j->i < j->n && (j->s[j->i] == '+' || j->s[j->i] == '-')) j->i++;
        if (j->i >= j->n || j->s[j->i] < '0' || j->s[j->i] > '9') return uj_fail(j, "in exponent of numeric literal");
        while (j->i < j->n && j->s[j->i] >= '0' && j->s[j->i] <= '9') j->i++;
    }
    char buf[400];
    size_t len = j->i - start;
    if (len >= sizeof buf) len = sizeof buf - 1;
    memcpy(buf, j->s + start, len);
    buf[len] = 0;
    double v = strtod(buf, NULL);
    if (trunc(v) == v) {
        /* int64(v) on amd64 yields INT64_MIN when out of range. */
        if (v >= 9223372036854775808.0 || v < -9223372036854775808.0) *out = uv_int(INT64_MIN);
        else *out = uv_int((int64_t)v);
    } else {
        *out = uv_float(v);
    }
    return true;
}

static bool uj_literal(UJson *j, const char *word, UValue v, UValue *out) {
    size_t n = strlen(word);
    for (size_t k = 0; k < n; k++) {
        if (j->i + k >= j->n) { j->i = j->n; return uj_fail(j, ""); }
        if (j->s[j->i + k] != word[k]) {
            j->i += k;
            char ctx[64];
            snprintf(ctx, sizeof ctx, "in literal %s (expecting '%c')", word, word[k]);
            return uj_fail(j, ctx);
        }
    }
    j->i += n;
    *out = v;
    return true;
}

static bool uj_value(UJson *j, UValue *out) {
    uj_skip(j);
    if (j->i >= j->n) return uj_fail(j, "");
    char c = j->s[j->i];
    switch (c) {
    case '{': {
        j->i++;
        UMap *m = u_map_new();
        uj_skip(j);
        if (j->i < j->n && j->s[j->i] == '}') { j->i++; *out = uv_mapv(m); return true; }
        for (;;) {
            uj_skip(j);
            if (j->i >= j->n || j->s[j->i] != '"') return uj_fail(j, "looking for beginning of object key string");
            UValue key, val;
            if (!uj_string(j, &key)) return false;
            uj_skip(j);
            if (j->i >= j->n || j->s[j->i] != ':') return uj_fail(j, "after object key");
            j->i++;
            if (!uj_value(j, &val)) return false;
            u_map_set(m, key, val);
            uj_skip(j);
            if (j->i < j->n && j->s[j->i] == ',') { j->i++; continue; }
            if (j->i < j->n && j->s[j->i] == '}') { j->i++; break; }
            return uj_fail(j, "after object key:value pair");
        }
        *out = uv_mapv(m);
        return true;
    }
    case '[': {
        j->i++;
        UArr *a = u_arr_new(0);
        uj_skip(j);
        if (j->i < j->n && j->s[j->i] == ']') { j->i++; *out = uv_arrv(a); return true; }
        for (;;) {
            UValue v;
            if (!uj_value(j, &v)) return false;
            u_arr_push(a, v);
            uj_skip(j);
            if (j->i < j->n && j->s[j->i] == ',') { j->i++; continue; }
            if (j->i < j->n && j->s[j->i] == ']') { j->i++; break; }
            return uj_fail(j, "after array element");
        }
        *out = uv_arrv(a);
        return true;
    }
    case '"': return uj_string(j, out);
    case 't': return uj_literal(j, "true", uv_bool(true), out);
    case 'f': return uj_literal(j, "false", uv_bool(false), out);
    case 'n': return uj_literal(j, "null", uv_nil(), out);
    default:
        if (c == '-' || (c >= '0' && c <= '9')) return uj_number(j, out);
        return uj_fail(j, "looking for beginning of value");
    }
}

U_BUILTIN_FN(uj_parse) {
    (void)env_;
    U_ARGC("json.parse", 1);
    UStr *s = u_want_str(argv[0], "json.parse input");
    UJson j = {s->data, s->len, 0, {0}};
    UValue v;
    if (!uj_value(&j, &v)) u_panic("json.parse failed: %s", j.err);
    uj_skip(&j);
    if (j.i < j.n) { uj_fail(&j, "after top-level value"); u_panic("json.parse failed: %s", j.err); }
    return v;
}

/* ---------- math ---------- */

static double u_want_num(UValue v, const char *name) {
    if (!u_is_num(v)) u_panic("%s expects number, got %s", name, u_type_name(v));
    return u_num(v);
}

#define U_MATH1(cname, uname, expr) \
    U_BUILTIN_FN(cname) { (void)env_; U_ARGC(uname, 1); double x = u_want_num(argv[0], uname); return uv_float(expr); }
#define U_MATH2(cname, uname, expr) \
    U_BUILTIN_FN(cname) { (void)env_; U_ARGC(uname, 2); double x = u_want_num(argv[0], uname); double y = u_want_num(argv[1], uname); return uv_float(expr); }

static double u_go_max(double x, double y) {
    if (isinf(x) && x > 0) return x;
    if (isinf(y) && y > 0) return y;
    if (isnan(x) || isnan(y)) return NAN;
    if (x == 0 && x == y) return signbit(x) ? y : x;
    return x > y ? x : y;
}

static double u_go_min(double x, double y) {
    if (isinf(x) && x < 0) return x;
    if (isinf(y) && y < 0) return y;
    if (isnan(x) || isnan(y)) return NAN;
    if (x == 0 && x == y) return signbit(x) ? x : y;
    return x < y ? x : y;
}

U_MATH1(umath_abs, "math.abs", fabs(x))
U_MATH1(umath_ceil, "math.ceil", ceil(x))
U_MATH1(umath_floor, "math.floor", floor(x))
U_MATH1(umath_round, "math.round", round(x))
U_MATH1(umath_sqrt, "math.sqrt", sqrt(x))
U_MATH2(umath_max, "math.max", u_go_max(x, y))
U_MATH2(umath_min, "math.min", u_go_min(x, y))
U_MATH2(umath_pow, "math.pow", pow(x, y))

/* ---------- time ---------- */

U_BUILTIN_FN(ut_now) {
    (void)env_; (void)argv;
    if (argc != 0) u_panic("time.now expects 0 arguments, got %d", argc);
    time_t now = time(NULL);
    struct tm tm;
    localtime_r(&now, &tm);
    char buf[64];
    strftime(buf, sizeof buf, "%Y-%m-%dT%H:%M:%S", &tm);
    long off = tm.tm_gmtoff;
    USb sb = {0};
    sb_puts(&sb, buf);
    if (off == 0) sb_putc(&sb, 'Z');
    else {
        char sign = off < 0 ? '-' : '+';
        if (off < 0) off = -off;
        sb_printf(&sb, "%c%02ld:%02ld", sign, off / 3600, (off / 60) % 60);
    }
    return uv_sb_value(&sb);
}

U_BUILTIN_FN(ut_unix) {
    (void)env_; (void)argv;
    if (argc != 0) u_panic("time.unix expects 0 arguments, got %d", argc);
    return uv_int((int64_t)time(NULL));
}

U_BUILTIN_FN(ut_format) {
    (void)env_; (void)argc; (void)argv;
    u_panic("time.format is not supported by the C backend yet");
}

/* ---------- process / shell ---------- */

U_BUILTIN_FN(up_args) {
    (void)env_; (void)argv;
    if (argc != 0) u_panic("process.args expects 0 arguments, got %d", argc);
    UArr *a = u_arr_new((size_t)u_argc);
    for (int i = 0; i < u_argc; i++) u_arr_push(a, uv_cstr(u_argv[i]));
    return uv_arrv(a);
}

U_BUILTIN_FN(up_env) {
    (void)env_;
    U_ARGC("process.env", 1);
    UStr *name = u_want_str(argv[0], "process.env name");
    const char *v = getenv(name->data);
    return uv_cstr(v ? v : "");
}

static bool u_is_executable(const char *path) {
    struct stat st;
    return stat(path, &st) == 0 && !S_ISDIR(st.st_mode) && (st.st_mode & 0111);
}

/* Resolves a command like exec.LookPath; returns NULL and fills err on failure. */
static char *u_lookpath(const char *name, USb *err) {
    if (strchr(name, '/')) {
        struct stat st;
        if (stat(name, &st) != 0) {
            sb_printf(err, "exec: \"%s\": stat %s: ", name, name);
            u_errno_text(err, errno);
            return NULL;
        }
        if (S_ISDIR(st.st_mode) || !(st.st_mode & 0111)) {
            sb_printf(err, "exec: \"%s\": permission denied", name);
            return NULL;
        }
        return strdup(name);
    }
    const char *path = getenv("PATH");
    if (path) {
        const char *p = path;
        for (;;) {
            const char *colon = strchr(p, ':');
            size_t n = colon ? (size_t)(colon - p) : strlen(p);
            USb cand = {0};
            if (n == 0) sb_puts(&cand, ".");
            else sb_putn(&cand, p, n);
            sb_putc(&cand, '/');
            sb_puts(&cand, name);
            if (u_is_executable(cand.buf)) return cand.buf;
            free(cand.buf);
            if (!colon) break;
            p = colon + 1;
        }
    }
    sb_printf(err, "exec: \"%s\": executable file not found in $PATH", name);
    return NULL;
}

static void u_trim_into(USb *dst, const char *s, size_t n) {
    UStr *tmp = u_str_new(s, n);
    UValue arg = uv_strv(tmp);
    UValue t = us_trim(NULL, 1, &arg);
    sb_putn(dst, t.as.s->data, t.as.s->len);
}

static UValue u_run_command(const char *who, char **cargv) {
    USb err = {0};
    char *exe = u_lookpath(cargv[0], &err);
    if (!exe) u_panic("%s failed: %s: ", who, err.buf);
    int fds[2];
    if (pipe(fds) != 0) u_panic("%s failed: pipe: %s", who, strerror(errno));
    fflush(stdout);
    fflush(stderr);
    pid_t pid = fork();
    if (pid < 0) u_panic("%s failed: fork: %s", who, strerror(errno));
    if (pid == 0) {
        int devnull = open("/dev/null", O_RDONLY);
        if (devnull >= 0) { dup2(devnull, 0); close(devnull); }
        dup2(fds[1], 1);
        dup2(fds[1], 2);
        close(fds[0]);
        close(fds[1]);
        execv(exe, cargv);
        _exit(127);
    }
    close(fds[1]);
    USb out = {0};
    char buf[8192];
    ssize_t n;
    while ((n = read(fds[0], buf, sizeof buf)) > 0) sb_putn(&out, buf, (size_t)n);
    close(fds[0]);
    int status = 0;
    while (waitpid(pid, &status, 0) < 0 && errno == EINTR) {}
    free(exe);
    if (WIFEXITED(status) && WEXITSTATUS(status) == 0) return uv_sb_value(&out);
    USb msg = {0};
    if (WIFEXITED(status)) sb_printf(&msg, "exit status %d", WEXITSTATUS(status));
    else if (WIFSIGNALED(status)) sb_printf(&msg, "signal: %s", strsignal(WTERMSIG(status)));
    else sb_puts(&msg, "unknown error");
    USb trimmed = {0};
    u_trim_into(&trimmed, out.buf ? out.buf : "", out.len);
    u_panic("%s failed: %s: %s", who, msg.buf, trimmed.buf ? trimmed.buf : "");
}

U_BUILTIN_FN(up_run) {
    (void)env_;
    if (argc < 1) u_panic("process.run expects at least 1 argument, got %d", argc);
    char **cargv = u_xalloc((size_t)(argc + 1) * sizeof(char *));
    cargv[0] = u_want_str(argv[0], "process.run command")->data;
    for (int i = 1; i < argc; i++) cargv[i] = u_want_str(argv[i], "process.run argument")->data;
    return u_run_command("process.run", cargv);
}

U_BUILTIN_FN(ush_run) {
    (void)env_;
    U_ARGC("shell.run", 1);
    char *cargv[4] = {"sh", "-c", u_want_str(argv[0], "shell.run command")->data, NULL};
    return u_run_command("shell.run", cargv);
}

/* ---------- module tables ---------- */

typedef struct { const char *name; UCode code; const char *qualified; } UBuiltinDef;

static const UBuiltinDef u_strings_defs[] = {
    {"contains", us_contains, "strings.contains"}, {"has_prefix", us_has_prefix, "strings.has_prefix"},
    {"has_suffix", us_has_suffix, "strings.has_suffix"}, {"join", us_join, "strings.join"},
    {"lower", us_lower, "strings.lower"}, {"replace", us_replace, "strings.replace"},
    {"split", us_split, "strings.split"}, {"trim", us_trim, "strings.trim"}, {"upper", us_upper, "strings.upper"},
    {NULL, NULL, NULL}};
static const UBuiltinDef u_arrays_defs[] = {
    {"append", ua_append, "arrays.append"}, {"filter", ua_filter, "arrays.filter"}, {"first", ua_first, "arrays.first"},
    {"join", ua_join, "arrays.join"}, {"last", ua_last, "arrays.last"}, {"map", ua_map, "arrays.map"},
    {"range", ua_range, "arrays.range"}, {"reduce", ua_reduce, "arrays.reduce"}, {"reverse", ua_reverse, "arrays.reverse"},
    {"slice", ua_slice, "arrays.slice"}, {NULL, NULL, NULL}};
static const UBuiltinDef u_maps_defs[] = {
    {"get", um_get, "maps.get"}, {"has", um_has, "maps.has"}, {"keys", um_keys, "maps.keys"},
    {"merge", um_merge, "maps.merge"}, {"set", um_set, "maps.set"}, {"values", um_values, "maps.values"},
    {NULL, NULL, NULL}};
static const UBuiltinDef u_json_defs[] = {
    {"parse", uj_parse, "json.parse"}, {"stringify", uj_stringify, "json.stringify"}, {NULL, NULL, NULL}};
static const UBuiltinDef u_math_defs[] = {
    {"abs", umath_abs, "math.abs"}, {"ceil", umath_ceil, "math.ceil"}, {"floor", umath_floor, "math.floor"},
    {"max", umath_max, "math.max"}, {"min", umath_min, "math.min"}, {"pow", umath_pow, "math.pow"},
    {"round", umath_round, "math.round"}, {"sqrt", umath_sqrt, "math.sqrt"}, {NULL, NULL, NULL}};
static const UBuiltinDef u_time_defs[] = {
    {"format", ut_format, "time.format"}, {"now", ut_now, "time.now"}, {"unix", ut_unix, "time.unix"}, {NULL, NULL, NULL}};
static const UBuiltinDef u_process_defs[] = {
    {"args", up_args, "process.args"}, {"env", up_env, "process.env"}, {"run", up_run, "process.run"}, {NULL, NULL, NULL}};
static const UBuiltinDef u_shell_defs[] = {{"run", ush_run, "shell.run"}, {NULL, NULL, NULL}};

static UValue u_std_module(const char *name) {
    const UBuiltinDef *defs = NULL;
    if (!strcmp(name, "strings")) defs = u_strings_defs;
    else if (!strcmp(name, "arrays")) defs = u_arrays_defs;
    else if (!strcmp(name, "maps")) defs = u_maps_defs;
    else if (!strcmp(name, "json")) defs = u_json_defs;
    else if (!strcmp(name, "math")) defs = u_math_defs;
    else if (!strcmp(name, "time")) defs = u_time_defs;
    else if (!strcmp(name, "process")) defs = u_process_defs;
    else if (!strcmp(name, "shell")) defs = u_shell_defs;
    else u_panic("unknown standard module %s", name);
    int n = 0;
    while (defs[n].name) n++;
    UMod *m = u_alloc(sizeof(UMod));
    m->name = name;
    m->count = n;
    m->names = u_alloc((size_t)n * sizeof(char *));
    m->values = u_alloc((size_t)n * sizeof(UValue));
    for (int i = 0; i < n; i++) {
        m->names[i] = defs[i].name;
        m->values[i] = u_builtin(defs[i].code, defs[i].qualified);
    }
    return uv_modv(m);
}

static UValue u_global_builtin(const char *name) {
    static const UBuiltinDef defs[] = {
        {"print", ub_print, "print"}, {"println", ub_println, "println"}, {"len", ub_len, "len"},
        {"type", ub_type, "type"}, {"read_file", ub_read_file, "read_file"},
        {"write_file", ub_write_file, "write_file"}, {"append_file", ub_append_file, "append_file"},
        {NULL, NULL, NULL}};
    for (int i = 0; defs[i].name; i++)
        if (!strcmp(defs[i].name, name)) return u_builtin(defs[i].code, defs[i].qualified);
    u_panic("identifier not found: %s", name);
}

/* Builds a module value from a top-level frame, skipping bindings that
 * never ran (the interpreter only exports names it actually defined). */
static UValue u_module_from_frame(const char *name, int count, const char **names, const int *slots, UFrame *frame) {
    UMod *m = u_alloc(sizeof(UMod));
    m->name = name;
    m->names = u_alloc((size_t)(count ? count : 1) * sizeof(char *));
    m->values = u_alloc((size_t)(count ? count : 1) * sizeof(UValue));
    for (int i = 0; i < count; i++) {
        UValue v = frame->slots[slots[i]];
        if (v.tag == U_UNDEF) continue;
        m->names[m->count] = names[i];
        m->values[m->count] = v;
        m->count++;
    }
    return uv_modv(m);
}

/* ---------- entry point ---------- */

static UValue u_program_main(void);
static void u_register_roots(void);

static void *u_main_thread(void *arg) {
    (void)arg;
    volatile char top = 0;
    u_stack_hi = (char *)&top;
    const char *threshold = getenv("UNDA_GC_THRESHOLD");
    if (threshold && atol(threshold) > 0) u_gc_min = u_gc_threshold = (size_t)atol(threshold);
    u_register_roots();
    u_gc_enabled = true;
    u_program_main();
    fflush(stdout);
    if (getenv("UNDA_GC_STATS"))
        fprintf(stderr, "gc: %zu collections, %zu live blocks\n", u_gc_collections, u_gc_count);
    return NULL;
}

int main(int argc, char **argv) {
    u_argc = argc;
    u_argv = argv;
    /* Run on a thread with a large stack so deep (bounded) recursion fits. */
    pthread_attr_t attr;
    pthread_attr_init(&attr);
    pthread_attr_setstacksize(&attr, (size_t)1 << 30);
    pthread_t th;
    if (pthread_create(&th, &attr, u_main_thread, NULL) != 0) {
        u_main_thread(NULL);
        return 0;
    }
    pthread_join(th, NULL);
    fflush(stdout);
    return 0;
}
