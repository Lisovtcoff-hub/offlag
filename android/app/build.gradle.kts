import java.util.Properties

plugins {
    id("com.android.application")
    id("kotlin-android")
    // The Flutter Gradle Plugin must be applied after the Android and Kotlin Gradle plugins.
    id("dev.flutter.flutter-gradle-plugin")
}

android {
    namespace = "com.offlag.offlag"
    compileSdk = flutter.compileSdkVersion
    ndkVersion = flutter.ndkVersion

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_11
        targetCompatibility = JavaVersion.VERSION_11
    }

    kotlinOptions {
        jvmTarget = JavaVersion.VERSION_11.toString()
    }

    defaultConfig {
        // TODO: Specify your own unique Application ID (https://developer.android.com/studio/build/application-id.html).
        applicationId = "com.offlag.offlag"
        // You can update the following values to match your application needs.
        // For more information, see: https://flutter.dev/to/review-gradle-config.
        minSdk = flutter.minSdkVersion
        targetSdk = flutter.targetSdkVersion
        versionCode = flutter.versionCode
        versionName = flutter.versionName
    }

    val keystorePropsFile = rootProject.file("keystore.properties")
    val keystoreProps = Properties()
    if (keystorePropsFile.exists()) {
        keystorePropsFile.inputStream().use { keystoreProps.load(it) }
    }

    val isReleaseBuild = gradle.startParameter.taskNames.any { it.contains("release", ignoreCase = true) }

    signingConfigs {
        if (keystorePropsFile.exists()) {
            create("release") {
                val storeFilePath = keystoreProps["storeFile"] as String?
                val storePasswordValue = keystoreProps["storePassword"] as String?
                val keyAliasValue = keystoreProps["keyAlias"] as String?
                val keyPasswordValue = keystoreProps["keyPassword"] as String?
                if (storeFilePath.isNullOrBlank() ||
                    storePasswordValue.isNullOrBlank() ||
                    keyAliasValue.isNullOrBlank() ||
                    keyPasswordValue.isNullOrBlank()
                ) {
                    throw GradleException("keystore.properties is missing required values: storeFile, storePassword, keyAlias, keyPassword")
                }
                storeFile = file(storeFilePath)
                storePassword = storePasswordValue
                keyAlias = keyAliasValue
                keyPassword = keyPasswordValue
            }
        } else if (isReleaseBuild) {
            throw GradleException("Missing keystore.properties for release signing")
        }
    }

    buildTypes {
        release {
            if (keystorePropsFile.exists()) {
                signingConfig = signingConfigs.getByName("release")
            }
        }
    }
}

flutter {
    source = "../.."
}
