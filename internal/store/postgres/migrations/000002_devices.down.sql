-- ============================================================
-- Migration 000002: Rollback Devices and Config Tables
-- ============================================================

DROP TABLE IF EXISTS command_queue CASCADE;
DROP TABLE IF EXISTS firmware_upgrade_tasks CASCADE;
DROP TABLE IF EXISTS firmware CASCADE;
DROP TABLE IF EXISTS device_overrides CASCADE;
DROP TABLE IF EXISTS device_configs CASCADE;
DROP TABLE IF EXISTS config_templates CASCADE;
DROP TABLE IF EXISTS devices CASCADE;
