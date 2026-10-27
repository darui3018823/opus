cmake_minimum_required(VERSION 3.16)
project(opus_workspace LANGUAGES C)

# CLion needs the reference sources' real compile options for navigation.
# libopus is optional, local-only, and never linked into the Go implementation.
if(EXISTS "${CMAKE_CURRENT_SOURCE_DIR}/libopus/CMakeLists.txt")
    add_subdirectory(libopus EXCLUDE_FROM_ALL)
endif()

find_program(GO_EXECUTABLE NAMES go)
if(GO_EXECUTABLE)
    function(add_go_target name)
        add_custom_target(${name}
            COMMAND "${GO_EXECUTABLE}" ${ARGN}
            WORKING_DIRECTORY "${CMAKE_CURRENT_SOURCE_DIR}"
            USES_TERMINAL
            VERBATIM)
    endfunction()

    add_go_target(go-build build ./...)
    add_go_target(go-vet vet ./...)
    add_go_target(go-test test -count=1 ./...)
    add_go_target(go-test-short test -short -count=1 ./...)
    add_go_target(go-test-coverage test -cover -count=1 ./...)
    add_go_target(go-benchmarks test -run=^$ -bench=. -benchmem -benchtime=10x ./...)
endif()
