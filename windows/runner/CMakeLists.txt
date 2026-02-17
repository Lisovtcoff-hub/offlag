cmake_minimum_required(VERSION 3.14)
project(runner LANGUAGES CXX)

# Define the application target. To change its name, change BINARY_NAME in the
# top-level CMakeLists.txt, not the value here, or `flutter run` will no longer
# work.
#
# Any new source files that you add to the application should be added here.
add_executable(${BINARY_NAME} WIN32
  "flutter_window.cpp"
  "main.cpp"
  "utils.cpp"
  "win32_window.cpp"
  "${FLUTTER_MANAGED_DIR}/generated_plugin_registrant.cc"
  "Runner.rc"
  "runner.exe.manifest"
)

# Apply the standard set of build settings. This can be removed for applications
# that need different build settings.
apply_standard_settings(${BINARY_NAME})

# Add preprocessor definitions for the build version.
target_compile_definitions(${BINARY_NAME} PRIVATE "FLUTTER_VERSION=\"${FLUTTER_VERSION}\"")
target_compile_definitions(${BINARY_NAME} PRIVATE "FLUTTER_VERSION_MAJOR=${FLUTTER_VERSION_MAJOR}")
target_compile_definitions(${BINARY_NAME} PRIVATE "FLUTTER_VERSION_MINOR=${FLUTTER_VERSION_MINOR}")
target_compile_definitions(${BINARY_NAME} PRIVATE "FLUTTER_VERSION_PATCH=${FLUTTER_VERSION_PATCH}")
target_compile_definitions(${BINARY_NAME} PRIVATE "FLUTTER_VERSION_BUILD=${FLUTTER_VERSION_BUILD}")

# Disable Windows macros that collide with C++ standard library functions.
target_compile_definitions(${BINARY_NAME} PRIVATE "NOMINMAX")

# Add dependency libraries and include directories. Add any application-specific
# dependencies here.
target_link_libraries(${BINARY_NAME} PRIVATE flutter flutter_wrapper_app)
target_link_libraries(${BINARY_NAME} PRIVATE "dwmapi.lib")
target_include_directories(${BINARY_NAME} PRIVATE "${CMAKE_SOURCE_DIR}")
target_link_options(${BINARY_NAME} PRIVATE "/MANIFESTUAC:level='requireAdministrator' uiAccess='false'")

# Run the Flutter tool portions of the build. This must not be removed.
add_dependencies(${BINARY_NAME} flutter_assemble)

# Copy Xray runtime files into the build output.
set(XRAY_BIN_DIR "${CMAKE_CURRENT_LIST_DIR}/../../third_party/xray/windows-amd64")
file(TO_CMAKE_PATH "${XRAY_BIN_DIR}" XRAY_BIN_DIR)
if (EXISTS "${XRAY_BIN_DIR}/xray.exe")
  add_custom_command(TARGET ${BINARY_NAME} POST_BUILD
    COMMAND ${CMAKE_COMMAND} -E copy_if_different
      "${XRAY_BIN_DIR}/xray.exe"
      "$<TARGET_FILE_DIR:${BINARY_NAME}>/xray.exe"
  )
else()
  message(WARNING "xray.exe not found at ${XRAY_BIN_DIR}")
endif()

if (EXISTS "${XRAY_BIN_DIR}/wintun.dll")
  add_custom_command(TARGET ${BINARY_NAME} POST_BUILD
    COMMAND ${CMAKE_COMMAND} -E copy_if_different
      "${XRAY_BIN_DIR}/wintun.dll"
      "$<TARGET_FILE_DIR:${BINARY_NAME}>/wintun.dll"
  )
else()
  message(WARNING "wintun.dll not found at ${XRAY_BIN_DIR}")
endif()

if (EXISTS "${XRAY_BIN_DIR}/geoip.dat")
  add_custom_command(TARGET ${BINARY_NAME} POST_BUILD
    COMMAND ${CMAKE_COMMAND} -E copy_if_different
      "${XRAY_BIN_DIR}/geoip.dat"
      "$<TARGET_FILE_DIR:${BINARY_NAME}>/geoip.dat"
  )
else()
  message(WARNING "geoip.dat not found at ${XRAY_BIN_DIR}")
endif()

if (EXISTS "${XRAY_BIN_DIR}/geosite.dat")
  add_custom_command(TARGET ${BINARY_NAME} POST_BUILD
    COMMAND ${CMAKE_COMMAND} -E copy_if_different
      "${XRAY_BIN_DIR}/geosite.dat"
      "$<TARGET_FILE_DIR:${BINARY_NAME}>/geosite.dat"
  )
else()
  message(WARNING "geosite.dat not found at ${XRAY_BIN_DIR}")
endif()
