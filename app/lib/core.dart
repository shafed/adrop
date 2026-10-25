import 'dart:convert';
import 'dart:ffi';
import 'dart:io';
import 'dart:isolate';

import 'package:ffi/ffi.dart';
import 'package:flutter/services.dart';

/// The Go core: on Android through the platform channel in MainActivity.kt,
/// on Linux through libadrop_core.so (core/cshared).
abstract class Core {
  factory Core() => Platform.isAndroid ? _ChannelCore() : _FfiCore();

  /// Opens or creates this device's identity; returns its fingerprint.
  Future<String> start();

  /// Trusts the device behind an adrop://pair URI; returns its name.
  Future<String> pair(String uri);

  /// Names of trusted devices.
  Future<List<String>> peers();

  /// Exchanges Hello with a trusted device; returns "name fingerprint".
  Future<String> hello(String peer);
}

class CoreException implements Exception {
  CoreException(this.message);
  final String message;
  @override
  String toString() => message;
}

List<String> _lines(String s) => s.isEmpty ? [] : s.split('\n');

class _ChannelCore implements Core {
  static const _channel = MethodChannel('adrop/core');

  Future<String> _call(String method, [String? arg]) async {
    try {
      return await _channel.invokeMethod<String>(method, arg) ?? '';
    } on PlatformException catch (e) {
      throw CoreException(e.message ?? e.code);
    }
  }

  @override
  Future<String> start() => _call('start');
  @override
  Future<String> pair(String uri) => _call('pair', uri);
  @override
  Future<List<String>> peers() async => _lines(await _call('peers'));
  @override
  Future<String> hello(String peer) => _call('hello', peer);
}

typedef _Fn0 = Pointer<Utf8> Function();
typedef _Fn1 = Pointer<Utf8> Function(Pointer<Utf8>);
typedef _Fn2 = Pointer<Utf8> Function(Pointer<Utf8>, Pointer<Utf8>);
typedef _Free = Void Function(Pointer<Utf8>);
typedef _FreeDart = void Function(Pointer<Utf8>);

/// Every call opens the library in the isolate that runs it: Go keeps one
/// core per process, so isolates share it.
class _FfiCore implements Core {
  static DynamicLibrary _open() {
    final dir = File(Platform.resolvedExecutable).parent.path;
    return DynamicLibrary.open('$dir/lib/libadrop_core.so');
  }

  static String _unwrap(DynamicLibrary lib, Pointer<Utf8> raw) {
    final free = lib.lookupFunction<_Free, _FreeDart>('adrop_free');
    final text = raw.toDartString();
    free(raw);
    final reply = jsonDecode(text) as Map<String, dynamic>;
    if (reply['ok'] != true) throw CoreException(reply['error'] as String);
    return (reply['result'] as String?) ?? '';
  }

  static Future<String> _call(String fn, List<String> args) => Isolate.run(() {
    final lib = _open();
    final ptrs = args.map((a) => a.toNativeUtf8()).toList();
    try {
      final Pointer<Utf8> raw = switch (ptrs.length) {
        0 => lib.lookupFunction<_Fn0, _Fn0>(fn)(),
        1 => lib.lookupFunction<_Fn1, _Fn1>(fn)(ptrs[0]),
        _ => lib.lookupFunction<_Fn2, _Fn2>(fn)(ptrs[0], ptrs[1]),
      };
      return _unwrap(lib, raw);
    } finally {
      for (final p in ptrs) {
        malloc.free(p);
      }
    }
  });

  @override
  Future<String> start() {
    final env = Platform.environment;
    final data = env['XDG_DATA_HOME'] ?? '${env['HOME']}/.local/share';
    return _call('adrop_start', [
      '$data/adrop-proto',
      '${Platform.localHostname} proto',
    ]);
  }

  @override
  Future<String> pair(String uri) => _call('adrop_pair', [uri]);
  @override
  Future<List<String>> peers() async => _lines(await _call('adrop_peers', []));
  @override
  Future<String> hello(String peer) => _call('adrop_hello', [peer]);
}
