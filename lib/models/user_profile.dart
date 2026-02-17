class UserProfile {
  final int id;
  final String email;
  final String nickname;
  final double balance;
  final DateTime? lastLogin;
  final DateTime? createdAt;

  final String planCode;
  final String planName;
  final double? monthlyPrice;
  final double? priceOverride;
  final double effectivePrice;
  final int activeSessions;
  final bool premiumActive;
  final int premiumUsersLeft;
  final int premiumDaysLeft;
  final DateTime? premiumExpiresAt;
  final bool yookassaEnabled;

  UserProfile({
    required this.id,
    required this.email,
    required this.nickname,
    required this.balance,
    required this.lastLogin,
    required this.createdAt,
    required this.planCode,
    required this.planName,
    required this.monthlyPrice,
    required this.priceOverride,
    required this.effectivePrice,
    required this.activeSessions,
    required this.premiumActive,
    required this.premiumUsersLeft,
    required this.premiumDaysLeft,
    required this.premiumExpiresAt,
    required this.yookassaEnabled,
  });

  factory UserProfile.fromMap(Map m) {
    DateTime? _dt(v) {
      if (v == null || '$v'.isEmpty) return null;
      try { return DateTime.parse('$v'); } catch (_) { return null; }
    }

    double? _numOrNull(v) {
      if (v == null) return null;
      return double.tryParse('$v');
    }

    final plan = (m['plan'] ?? {}) as Map;
    final premium = (m['premium'] ?? {}) as Map;
    final payments = (m['payments'] ?? {}) as Map;

    return UserProfile(
      id: int.tryParse('${m['id'] ?? 0}') ?? 0,
      email: (m['email'] ?? '') as String,
      nickname: (m['nickname'] ?? '') as String,
      balance: double.tryParse('${m['balance'] ?? 0}') ?? 0.0,
      lastLogin: _dt(m['last_login']),
      createdAt: _dt(m['created_at']),
      planCode: (plan['code'] ?? '') as String,
      planName: (plan['name'] ?? '') as String,
      monthlyPrice: _numOrNull(m['monthly_price']),
      priceOverride: _numOrNull(m['price_override']),
      effectivePrice: double.tryParse('${m['effective_price'] ?? 0}') ?? 0.0,
      activeSessions: int.tryParse('${m['active_sessions'] ?? 0}') ?? 0,
      premiumActive: premium['active'] == true,
      premiumUsersLeft: int.tryParse('${premium['users_left'] ?? 0}') ?? 0,
      premiumDaysLeft: int.tryParse('${premium['days_left'] ?? 0}') ?? 0,
      premiumExpiresAt: _dt(premium['expires_at']),
      yookassaEnabled: payments['yookassa_enabled'] == true,
    );
  }

  String get formattedBalance =>
      balance.toStringAsFixed(balance.truncateToDouble() == balance ? 0 : 2);

  String get formattedEffective =>
      effectivePrice.toStringAsFixed(effectivePrice.truncateToDouble() == effectivePrice ? 0 : 2);
}
