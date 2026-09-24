# Add project specific ProGuard rules here.
# Preserve classes and members annotated with @Keep for R8
-keepattributes *Annotation*
-keep @androidx.annotation.Keep class * { *; }
-keepclassmembers class * {
    @androidx.annotation.Keep *;
}
