-- Enable required extensions
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pgcrypto";
CREATE EXTENSION IF NOT EXISTS timescaledb;

-- Create additional databases for testing
CREATE DATABASE cloudcontroller_test;

-- Grant permissions
GRANT ALL PRIVILEGES ON DATABASE cloudcontroller TO cloudctrl;
GRANT ALL PRIVILEGES ON DATABASE cloudcontroller_test TO cloudctrl;

-- Connect to test DB and enable extensions
\c cloudcontroller_test;
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS "pgcrypto";
CREATE EXTENSION IF NOT EXISTS timescaledb;
