import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:shared_preferences/shared_preferences.dart';

class TokenStore {
  static const _kToken = 'auth_token';
  static const _kEmail = 'auth_email';
  static const _kRefresh = 'refresh_token';
  static const FlutterSecureStorage _secure = FlutterSecureStorage(
    aOptions: AndroidOptions(encryptedSharedPreferences: true),
  );

  static Future<void> save(String token, String email, {String? refreshToken}) async {
    await _secure.write(key: _kToken, value: token);
    await _secure.write(key: _kEmail, value: email);
    if (refreshToken != null && refreshToken.isNotEmpty) {
      await _secure.write(key: _kRefresh, value: refreshToken);
    }
  }

  static Future<String?> get token async {
    final stored = await _secure.read(key: _kToken);
    if (stored != null && stored.isNotEmpty) return stored;

    // Migrate legacy value from SharedPreferences if present.
    final p = await SharedPreferences.getInstance();
    final legacy = p.getString(_kToken);
    if (legacy != null && legacy.isNotEmpty) {
      await _secure.write(key: _kToken, value: legacy);
      await p.remove(_kToken);
    }
    return legacy;
  }

  static Future<String?> get email async {
    final stored = await _secure.read(key: _kEmail);
    if (stored != null && stored.isNotEmpty) return stored;

    // Migrate legacy value from SharedPreferences if present.
    final p = await SharedPreferences.getInstance();
    final legacy = p.getString(_kEmail);
    if (legacy != null && legacy.isNotEmpty) {
      await _secure.write(key: _kEmail, value: legacy);
      await p.remove(_kEmail);
    }
    return legacy;
  }

  static Future<void> updateTokens(String token, {String? refreshToken}) async {
    final email = await TokenStore.email ?? '';
    await _secure.write(key: _kToken, value: token);
    if (refreshToken != null && refreshToken.isNotEmpty) {
      await _secure.write(key: _kRefresh, value: refreshToken);
    }
    if (email.isNotEmpty) {
      await _secure.write(key: _kEmail, value: email);
    }
  }

  static Future<String?> get refreshToken async {
    final stored = await _secure.read(key: _kRefresh);
    if (stored != null && stored.isNotEmpty) return stored;

    final p = await SharedPreferences.getInstance();
    final legacy = p.getString(_kRefresh);
    if (legacy != null && legacy.isNotEmpty) {
      await _secure.write(key: _kRefresh, value: legacy);
      await p.remove(_kRefresh);
    }
    return legacy;
  }

  static Future<void> clear() async {
    await _secure.delete(key: _kToken);
    await _secure.delete(key: _kEmail);
    await _secure.delete(key: _kRefresh);

    final p = await SharedPreferences.getInstance();
    await p.remove(_kToken);
    await p.remove(_kEmail);
    await p.remove(_kRefresh);
  }
}
