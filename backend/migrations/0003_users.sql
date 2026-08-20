-- ============================================================
-- 0003: users (authentication + RBAC)
-- ============================================================

CREATE TABLE IF NOT EXISTS users (
  id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  username      text UNIQUE NOT NULL,
  password_hash text NOT NULL,
  role          text NOT NULL DEFAULT 'viewer'
                CHECK (role IN ('admin', 'planner', 'operator', 'viewer')),
  is_active     boolean NOT NULL DEFAULT true,
  created_at    timestamptz NOT NULL DEFAULT now()
);

-- 初期ユーザーは backend 起動時に自動シードされます (users テーブルが空のときのみ)。
-- 既定: admin/admin123, planner/planner123, operator/operator123, viewer/viewer123
-- 本番では必ず変更してください。
