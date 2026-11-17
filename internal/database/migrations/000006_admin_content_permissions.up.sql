ALTER TABLE admin_roles
    DROP CONSTRAINT admin_roles_permissions_allowed;

ALTER TABLE admin_roles
    ADD CONSTRAINT admin_roles_permissions_allowed CHECK (permissions <@ ARRAY[
        'dashboard.view', 'products.view', 'products.review', 'products.edit', 'products.unpublish',
        'comments.view', 'comments.moderate', 'users.view', 'users.manage', 'audit.view',
        'organizations.view', 'organizations.manage', 'taxonomy.view', 'taxonomy.manage'
    ]::TEXT[]);