/*
** Shim standing in for Fossil's generated delta.h: the declarations of the
** four functions src/delta.c defines.
*/
#ifndef FDELTA_CREF_DELTA_H
#define FDELTA_CREF_DELTA_H

int delta_create(const char *zSrc, unsigned int lenSrc,
                 const char *zOut, unsigned int lenOut,
                 char *zDelta);

int delta_output_size(const char *zDelta, int lenDelta);

int delta_apply(const char *zSrc, int lenSrc,
                const char *zDelta, int lenDelta,
                char *zOut);

int delta_analyze(const char *zDelta, int lenDelta,
                  int *pnCopy, int *pnInsert);

#endif /* FDELTA_CREF_DELTA_H */
