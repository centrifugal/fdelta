/*
** Shim standing in for Fossil's generated config.h, so that src/delta.c can be
** compiled on its own. delta.c states that it depends on nothing else in
** Fossil, and these names are all it actually needs from the tree: the fixed
** width integer aliases Fossil defines, and its two allocation wrappers.
*/
#ifndef FDELTA_CREF_CONFIG_H
#define FDELTA_CREF_CONFIG_H

#include <stdint.h>
#include <stdlib.h>

typedef uint16_t u16;
typedef uint32_t u32;
typedef uint64_t u64;
typedef int64_t i64;

typedef uint64_t sqlite3_uint64;

#define fossil_malloc malloc
#define fossil_free free

#endif /* FDELTA_CREF_CONFIG_H */
