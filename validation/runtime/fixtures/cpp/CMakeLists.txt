# runtime-validation C++ fixture 构建单个无外部依赖的跨平台可执行文件。
cmake_minimum_required(VERSION 3.20)
project(superdev_runtime_validation_cpp LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
add_executable(superdev-runtime-validation-cpp src/main.cpp)
if(WIN32)
  target_link_libraries(superdev-runtime-validation-cpp PRIVATE ws2_32)
endif()
