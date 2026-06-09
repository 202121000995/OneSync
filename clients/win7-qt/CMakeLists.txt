cmake_minimum_required(VERSION 3.16)

project(OneSyncWin7Qt VERSION 0.1.0 LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_AUTOMOC ON)
set(CMAKE_AUTORCC ON)
set(CMAKE_AUTOUIC ON)

find_package(Qt5 5.12 REQUIRED COMPONENTS Widgets Network)

add_executable(OneSyncWin7 WIN32
    src/main.cpp
    src/MainWindow.cpp
    src/MainWindow.h
    src/SyncLink.cpp
    src/SyncLink.h
)

target_compile_definitions(OneSyncWin7 PRIVATE
    WINVER=0x0601
    _WIN32_WINNT=0x0601
    NOMINMAX
)

target_link_libraries(OneSyncWin7 PRIVATE
    Qt5::Widgets
    Qt5::Network
)
