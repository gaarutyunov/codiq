#include "greeter.h"

int main(void) {
    Greeter g;
    g.name = "world";
    greet(&g);
    return 0;
}
