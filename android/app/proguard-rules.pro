# gomobile bindings are called from native code through JNI: nothing in them
# may be renamed or removed, R8 can't see those calls
-keep class go.** { *; }
-keep class app.vela.libbox.** { *; }
-keep class app.vela.velacore.** { *; }
-keepclassmembers class * implements go.Seq$Proxy { *; }
# Go calls back into app code through the binding interfaces (platform
# interface, probe and speed test handlers, iterators, also lambdas and
# anonymous classes): keep the implementations of their methods
-keep class * implements app.vela.libbox.** { *; }
-keep class * implements app.vela.velacore.** { *; }
-keep class app.vela.client.vpn.** { *; }
-keepclasseswithmembernames,includedescriptorclasses class * { native <methods>; }
