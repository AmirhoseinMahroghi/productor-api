ALTER TABLE product_versions ADD COLUMN published_at TIMESTAMPTZ;
CREATE INDEX product_versions_published_idx ON product_versions(published_at DESC) WHERE status='published';
