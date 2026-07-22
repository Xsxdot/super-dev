# C++ Windows fixture build definition.
# Responsibility: produce one debug-symbol-rich Windows executable through frozen clang-cl and Ninja.
# Boundary: it does not download toolchains or define SuperDev/MCP orchestration.
cmake_minimum_required(VERSION 4.0)
project(superdev_windows_cpp_fixture LANGUAGES CXX)

add_executable(superdev-windows-cpp-fixture src/main.cpp)
target_compile_features(superdev-windows-cpp-fixture PRIVATE cxx_std_20)
if(MSVC)
  target_compile_options(superdev-windows-cpp-fixture PRIVATE /Zi /Od /EHsc /W4)
  target_link_options(superdev-windows-cpp-fixture PRIVATE /DEBUG:FULL)
endif()
target_link_libraries(superdev-windows-cpp-fixture PRIVATE ws2_32)
