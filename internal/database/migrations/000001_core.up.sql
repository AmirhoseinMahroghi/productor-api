CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    phone_e164 TEXT NOT NULL UNIQUE,
    role TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'moderator', 'admin')),
    is_blocked BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE otp_challenges (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    phone_e164 TEXT NOT NULL,
    code_hash BYTEA NOT NULL,
    request_ip TEXT NOT NULL DEFAULT '',
    attempts SMALLINT NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    requested_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX otp_phone_requested_idx ON otp_challenges(phone_e164, requested_at DESC);
CREATE INDEX otp_ip_requested_idx ON otp_challenges(request_ip, requested_at DESC);
CREATE TABLE sessions (
    token_hash BYTEA PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX sessions_user_idx ON sessions(user_id, expires_at DESC);
CREATE TABLE organizations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    logo TEXT NOT NULL DEFAULT '',
    bio TEXT NOT NULL DEFAULT '',
    website TEXT NOT NULL,
    links JSONB NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(links) = 'array'),
    created_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE organization_memberships (
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('owner', 'admin', 'editor')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, user_id)
);
CREATE INDEX memberships_user_idx ON organization_memberships(user_id, organization_id);
CREATE TABLE products (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    slug TEXT NOT NULL UNIQUE,
    current_version_id UUID,
    published_version_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX products_organization_idx ON products(organization_id, created_at DESC);
CREATE TABLE product_versions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    version_no INTEGER NOT NULL CHECK (version_no > 0),
    payload JSONB NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    status TEXT NOT NULL CHECK (status IN ('draft', 'submitted', 'in_review', 'changes_requested', 'rejected', 'published', 'unpublished', 'superseded')),
    created_by UUID NOT NULL REFERENCES users(id),
    reviewer_id UUID REFERENCES users(id),
    reviewer_note TEXT NOT NULL DEFAULT '',
    submitted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    reviewed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (product_id, version_no),
    UNIQUE (product_id, id)
);
ALTER TABLE products
    ADD CONSTRAINT products_current_version_fk
    FOREIGN KEY (id, current_version_id)
    REFERENCES product_versions(product_id, id)
    DEFERRABLE INITIALLY DEFERRED;
ALTER TABLE products
    ADD CONSTRAINT products_published_version_fk
    FOREIGN KEY (id, published_version_id)
    REFERENCES product_versions(product_id, id)
    DEFERRABLE INITIALLY DEFERRED;
CREATE INDEX product_versions_queue_idx ON product_versions(status, submitted_at ASC);
CREATE UNIQUE INDEX product_versions_single_published_idx ON product_versions(product_id) WHERE status = 'published';
CREATE TABLE review_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    version_id UUID REFERENCES product_versions(id) ON DELETE SET NULL,
    actor_id UUID REFERENCES users(id) ON DELETE SET NULL,
    actor_name TEXT NOT NULL,
    actor_role TEXT NOT NULL,
    action TEXT NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX review_events_product_idx ON review_events(product_id, created_at ASC);
CREATE TABLE votes (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    product_id UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, product_id)
);
CREATE INDEX votes_product_created_idx ON votes(product_id, created_at DESC);
CREATE TABLE comments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    product_id UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    body TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'published', 'rejected')),
    reject_reason TEXT NOT NULL DEFAULT '',
    edit_deadline TIMESTAMPTZ,
    reviewer_id UUID REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    reviewed_at TIMESTAMPTZ
);
CREATE INDEX comments_product_public_idx ON comments(product_id, created_at DESC) WHERE status = 'published';
CREATE INDEX comments_moderation_idx ON comments(status, created_at ASC);
CREATE INDEX comments_author_idx ON comments(user_id, created_at DESC);
CREATE TABLE notifications (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    title TEXT NOT NULL,
    body TEXT NOT NULL,
    url TEXT NOT NULL DEFAULT '',
    read_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX notifications_user_idx ON notifications(user_id, created_at DESC);
CREATE TABLE media_files (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    uploader_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    storage_key TEXT NOT NULL UNIQUE,
    content_type TEXT NOT NULL,
    file_size BIGINT NOT NULL CHECK (file_size > 0),
    is_public BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX media_org_idx ON media_files(organization_id, created_at DESC);
CREATE TABLE topics (
    slug TEXT PRIMARY KEY,
    label TEXT NOT NULL UNIQUE,
    tone SMALLINT NOT NULL CHECK (tone BETWEEN 1 AND 7),
    icon TEXT NOT NULL,
    position SMALLINT NOT NULL DEFAULT 0,
    is_active BOOLEAN NOT NULL DEFAULT TRUE
);
INSERT INTO topics(slug, label, tone, icon, position) VALUES
('task-management', 'مدیریت وظایف', 2, 'check', 1),
('project-management', 'مدیریت پروژه', 5, 'layers', 2),
('kanban', 'کانبان', 6, 'shape.frame', 3),
('team-chat', 'گفت‌وگوی تیمی', 7, 'comment', 4),
('knowledge-base', 'پایگاه دانش', 3, 'file', 5),
('video-meetings', 'جلسهٔ ویدیویی', 1, 'camera', 6),
('product-analytics', 'تحلیل محصول', 5, 'explore', 7),
('business-intelligence', 'هوش تجاری', 2, 'sparkles', 8),
('data-visualization', 'تصویرسازی داده', 6, 'layers', 9),
('cloud-infrastructure', 'زیرساخت ابری', 5, 'network', 10),
('container-management', 'مدیریت کانتینر', 6, 'shape.rect', 11),
('server-monitoring', 'پایش سرور', 3, 'status.info', 12),
('observability', 'مشاهده‌پذیری', 7, 'show', 13),
('log-management', 'مدیریت لاگ', 2, 'file.add', 14),
('ci-cd', 'CI/CD', 4, 'reset', 15),
('version-control', 'کنترل نسخه', 1, 'copy', 16),
('code-review', 'بازبینی کد', 6, 'edit', 17),
('customer-feedback', 'بازخورد مشتری', 5, 'comment', 18),
('event-management', 'مدیریت رویداد', 2, 'calendar', 19),
('note-taking', 'یادداشت‌برداری', 3, 'edit', 20),
('ai-assistant', 'دستیار هوش مصنوعی', 7, 'sparkles', 21),
('workflow-automation', 'خودکارسازی گردش‌کار', 5, 'settings', 22),
('ui-design', 'طراحی رابط کاربری', 1, 'pointer', 23),
('design-system', 'سیستم طراحی', 2, 'layers', 24),
('crm', 'CRM', 6, 'users', 25),
('identity-access', 'مدیریت هویت و دسترسی', 4, 'lock', 26),
('secrets-management', 'مدیریت اسرار', 1, 'lock', 27),
('cdn', 'شبکه توزیع محتوا', 2, 'network', 28),
('online-payment', 'پرداخت آنلاین', 2, 'product.funding', 29),
('payment-gateway', 'درگاه پرداخت', 4, 'link', 30),
('online-marketplace', 'بازارگاه آنلاین', 5, 'launcher', 31),
('online-shopping', 'خرید آنلاین', 6, 'search', 32),
('classifieds', 'آگهی رایگان', 3, 'file', 33),
('messaging', 'پیام‌رسان', 7, 'comment', 34),
('video-streaming', 'پخش ویدیو', 4, 'play', 35),
('video-subscription', 'اشتراک ویدیو', 1, 'media.queue', 36),
('online-learning', 'آموزش آنلاین', 3, 'media.podcast', 37),
('online-course', 'دورهٔ آنلاین', 2, 'play', 38),
('app-store', 'فروشگاه اپلیکیشن', 1, 'device.mobile', 39),
('mobile-games', 'بازی موبایل', 6, 'star', 40),
('customer-support', 'پشتیبانی مشتری', 4, 'reply', 41),
('transportation', 'حمل‌ونقل', 5, 'explore', 42),
('ride-hailing', 'درخواست خودرو', 7, 'pointer', 43),
('content-publishing', 'انتشار محتوا', 6, 'send', 44),
('blogging', 'وبلاگ‌نویسی', 3, 'edit', 45);