// Pure JVM module — no Android SDK needed.
// Contains the proto codec (shared logic) and its unit tests.
// Run with: ./gradlew :jvm-tests:test   (or the local Gradle binary)
plugins {
    alias(libs.plugins.kotlin.jvm)
    alias(libs.plugins.kotlin.serialization)
}

// Use the running JVM's version directly; do NOT request a specific toolchain
// so the build works without a provisioning network (the host may only have JDK 26+).
// The source / target compat are set explicitly to avoid requiring a JDK download.
java {
    sourceCompatibility = JavaVersion.VERSION_17
    targetCompatibility = JavaVersion.VERSION_17
}

kotlin {
    compilerOptions {
        jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17)
    }
}

dependencies {
    implementation(libs.kotlinx.serialization.json)
    implementation(libs.kotlinx.coroutines.core)
    testImplementation(libs.junit)
}
