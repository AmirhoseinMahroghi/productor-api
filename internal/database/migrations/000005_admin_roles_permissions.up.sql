CREATE TABLE admin_roles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    key TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL UNIQUE CHECK (char_length(trim(name)) BETWEEN 2 AND 80),
    description TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 240),
    permissions TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
    is_system BOOLEAN NOT NULL DEFAULT FALSE,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT admin_roles_permissions_allowed CHECK (permissions <@ ARRAY[
        'dashboard.view', 'products.view', 'products.review', 'products.edit', 'products.unpublish',
        'comments.view', 'comments.moderate', 'users.view', 'users.manage', 'audit.view'
    ]::TEXT[])
);
CREATE UNIQUE INDEX admin_roles_system_key_idx ON admin_roles(key) WHERE key <> '';
ALTER TABLE users ADD COLUMN admin_role_id UUID REFERENCES admin_roles(id) ON DELETE SET NULL;
CREATE INDEX users_admin_role_idx ON users(admin_role_id) WHERE admin_role_id IS NOT NULL;

INSERT INTO admin_roles(key, name, description, permissions, is_system)
VALUES (
    'moderator',
    'ناظر',
    'نقش پایه برای بررسی محصولات و نظرها.',
    ARRAY[
        'dashboard.view', 'products.view', 'products.review', 'products.edit', 'products.unpublish',
        'comments.view', 'comments.moderate'
    ]::TEXT[],
    TRUE
);
UPDATE users
SET admin_role_id = (SELECT id FROM admin_roles WHERE key = 'moderator')
WHERE role = 'moderator' AND admin_role_id IS NULL;