cmake_minimum_required(VERSION 3.20)

project(greeter VERSION 1.0.0 LANGUAGES C CXX)

add_library(greeter src/greeter.c)
target_include_directories(greeter PUBLIC include)

add_executable(hello src/main.c)
target_link_libraries(hello PRIVATE greeter)
