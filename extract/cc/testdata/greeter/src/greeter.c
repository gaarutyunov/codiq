#include "greeter.h"

#include <stdio.h>

static const char *fallback(void) {
    return "world";
}

void greet(const Greeter *g) {
    printf("%s, %s\n", GREETING, greeter_name(g));
}

const char *greeter_name(const Greeter *g) {
    if (g->name == NULL) {
        return fallback();
    }
    return g->name;
}
