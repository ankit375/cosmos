-- ============================================================
-- Rollback Migration 000004
-- ============================================================

-- Drop RLS policies
DROP POLICY IF EXISTS tenant_isolation ON tenants;
DROP POLICY IF EXISTS user_tenant_isolation ON users;
DROP POLICY IF EXISTS site_tenant_isolation ON sites;
DROP POLICY IF EXISTS device_tenant_isolation ON devices;
DROP POLICY IF EXISTS device_config_tenant_isolation ON device_configs;
DROP POLICY IF EXISTS device_override_tenant_isolation ON device_overrides;
DROP POLICY IF EXISTS config_template_tenant_isolation ON config_templates;
DROP POLICY IF EXISTS command_queue_tenant_isolation ON command_queue;
DROP POLICY IF EXISTS firmware_task_tenant_isolation ON firmware_upgrade_tasks;

-- Disable RLS
ALTER TABLE tenants DISABLE ROW LEVEL SECURITY;
ALTER TABLE users DISABLE ROW LEVEL SECURITY;
ALTER TABLE sites DISABLE ROW LEVEL SECURITY;
ALTER TABLE devices DISABLE ROW LEVEL SECURITY;
ALTER TABLE device_configs DISABLE ROW LEVEL SECURITY;
ALTER TABLE device_overrides DISABLE ROW LEVEL SECURITY;
ALTER TABLE config_templates DISABLE ROW LEVEL SECURITY;
ALTER TABLE command_queue DISABLE ROW LEVEL SECURITY;
ALTER TABLE firmware_upgrade_tasks DISABLE ROW LEVEL SECURITY;

-- Revert role constraint
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_role_check;
ALTER TABLE users ADD CONSTRAINT users_role_check
    CHECK (role IN ('admin', 'operator', 'viewer'));

-- Remove max_users column
ALTER TABLE tenants DROP COLUMN IF EXISTS max_users;

-- Drop indexes
DROP INDEX IF EXISTS idx_users_tenant_count;
DROP INDEX IF EXISTS idx_devices_tenant_count;
