#ifndef GREETER_H
#define GREETER_H

#include <stddef.h>

#define GREETING "hello"

typedef struct Greeter {
    const char *name;
} Greeter;

void greet(const Greeter *g);

const char *greeter_name(const Greeter *g);

#endif /* GREETER_H */
