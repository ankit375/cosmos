#!/bin/bash
set -e

GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo "============================================"
echo "  Cloud Controller Setup Verification"
echo "============================================"
echo ""

ERRORS=0

# Check Go
echo -n "Go:            "
if command -v go &> /dev/null; then
    echo -e "${GREEN}$(go version)${NC}"
else
    echo -e "${RED}NOT INSTALLED${NC}"
    ERRORS=$((ERRORS+1))
fi

# Check Docker
echo -n "Docker:        "
if command -v docker &> /dev/null; then
    echo -e "${GREEN}$(docker --version)${NC}"
else
    echo -e "${RED}NOT INSTALLED${NC}"
    ERRORS=$((ERRORS+1))
fi

# Check Docker Compose
echo -n "Compose:       "
if docker compose version &> /dev/null; then
    echo -e "${GREEN}$(docker compose version)${NC}"
else
    echo -e "${RED}NOT INSTALLED${NC}"
    ERRORS=$((ERRORS+1))
fi

# Check GCC
echo -n "GCC:           "
if command -v gcc &> /dev/null; then
    echo -e "${GREEN}$(gcc --version | head -1)${NC}"
else
    echo -e "${RED}NOT INSTALLED${NC}"
    ERRORS=$((ERRORS+1))
fi

# Check migrate
echo -n "Migrate:       "
if command -v migrate &> /dev/null; then
    echo -e "${GREEN}$(migrate --version 2>&1 || echo 'installed')${NC}"
else
    echo -e "${RED}NOT INSTALLED${NC}"
    ERRORS=$((ERRORS+1))
fi

# Check golangci-lint
echo -n "Linter:        "
if command -v golangci-lint &> /dev/null; then
    echo -e "${GREEN}$(golangci-lint --version 2>&1 | head -1)${NC}"
else
    echo -e "${YELLOW}NOT INSTALLED (optional)${NC}"
fi

# Check air
echo -n "Air:           "
if command -v air &> /dev/null; then
    echo -e "${GREEN}installed${NC}"
else
    echo -e "${YELLOW}NOT INSTALLED (optional - for live reload)${NC}"
fi

# Check websocat
echo -n "Websocat:      "
if command -v websocat &> /dev/null; then
    echo -e "${GREEN}installed${NC}"
else
    echo -e "${YELLOW}NOT INSTALLED (optional - for WS testing)${NC}"
fi

# Check OpenSSL
echo -n "OpenSSL:       "
if command -v openssl &> /dev/null; then
    echo -e "${GREEN}$(openssl version)${NC}"
else
    echo -e "${RED}NOT INSTALLED${NC}"
    ERRORS=$((ERRORS+1))
fi

# Check libwebsockets
echo -n "libwebsockets: "
if dpkg -l | grep -q libwebsockets-dev; then
    echo -e "${GREEN}installed${NC}"
else
    echo -e "${RED}NOT INSTALLED${NC}"
    ERRORS=$((ERRORS+1))
fi

# Check cJSON
echo -n "cJSON:         "
if dpkg -l | grep -q libcjson-dev; then
    echo -e "${GREEN}installed${NC}"
else
    echo -e "${RED}NOT INSTALLED${NC}"
    ERRORS=$((ERRORS+1))
fi

echo ""

# Check Docker services (if running)
echo "--- Docker Services ---"
if docker ps &> /dev/null; then
    echo -n "PostgreSQL:    "
    if docker ps | grep -q cloudctrl-postgres; then
        echo -e "${GREEN}RUNNING${NC}"
    else
        echo -e "${YELLOW}NOT RUNNING (run 'make docker-up')${NC}"
    fi
    
    echo -n "Redis:         "
    if docker ps | grep -q cloudctrl-redis; then
        echo -e "${GREEN}RUNNING${NC}"
    else
        echo -e "${YELLOW}NOT RUNNING (run 'make docker-up')${NC}"
    fi
    
    echo -n "MinIO:         "
    if docker ps | grep -q cloudctrl-minio; then
        echo -e "${GREEN}RUNNING${NC}"
    else
        echo -e "${YELLOW}NOT RUNNING (run 'make docker-up')${NC}"
    fi

    echo -n "Prometheus:    "
    if docker ps | grep -q cloudctrl-prometheus; then
        echo -e "${GREEN}RUNNING${NC}"
    else
        echo -e "${YELLOW}NOT RUNNING (run 'make docker-up')${NC}"
    fi
    
    echo -n "Grafana:       "
    if docker ps | grep -q cloudctrl-grafana; then
        echo -e "${GREEN}RUNNING${NC}"
    else
        echo -e "${YELLOW}NOT RUNNING (run 'make docker-up')${NC}"
    fi
else
    echo -e "${YELLOW}Docker not accessible${NC}"
fi

echo ""

# Check connectivity (if services running)
echo "--- Connectivity ---"
echo -n "PostgreSQL:    "
if pg_isready -h localhost -p 5432 -U cloudctrl &> /dev/null; then
    echo -e "${GREEN}CONNECTABLE${NC}"
else
    echo -e "${YELLOW}NOT REACHABLE${NC}"
fi

echo -n "Redis:         "
if redis-cli -a cloudctrl_redis_password ping 2>/dev/null | grep -q PONG; then
    echo -e "${GREEN}CONNECTABLE${NC}"
else
    echo -e "${YELLOW}NOT REACHABLE${NC}"
fi

echo -n "MinIO:         "
if curl -s -o /dev/null -w "%{http_code}" http://localhost:9000/minio/health/live 2>/dev/null | grep -q 200; then
    echo -e "${GREEN}CONNECTABLE${NC}"
else
    echo -e "${YELLOW}NOT REACHABLE${NC}"
fi

echo ""

# Check certificates
echo "--- TLS Certificates ---"
CERT_DIR="certs"
echo -n "CA Cert:       "
if [ -f "$CERT_DIR/ca.pem" ]; then
    echo -e "${GREEN}EXISTS${NC}"
else
    echo -e "${YELLOW}NOT FOUND (run 'make certs')${NC}"
fi

echo -n "Server Cert:   "
if [ -f "$CERT_DIR/server.pem" ]; then
    echo -e "${GREEN}EXISTS${NC}"
else
    echo -e "${YELLOW}NOT FOUND (run 'make certs')${NC}"
fi

echo -n "Server Key:    "
if [ -f "$CERT_DIR/server-key.pem" ]; then
    echo -e "${GREEN}EXISTS${NC}"
else
    echo -e "${YELLOW}NOT FOUND (run 'make certs')${NC}"
fi

echo ""

# Check project structure
echo "--- Project Structure ---"
echo -n "go.mod:        "
if [ -f "go.mod" ]; then
    echo -e "${GREEN}EXISTS${NC}"
else
    echo -e "${RED}NOT FOUND${NC}"
    ERRORS=$((ERRORS+1))
fi

echo -n "Config:        "
if [ -f "configs/controller.dev.yaml" ]; then
    echo -e "${GREEN}EXISTS${NC}"
else
    echo -e "${RED}NOT FOUND${NC}"
    ERRORS=$((ERRORS+1))
fi

echo ""

# Summary
echo "============================================"
if [ $ERRORS -eq 0 ]; then
    echo -e "${GREEN}All checks passed! Ready to develop.${NC}"
else
    echo -e "${RED}$ERRORS critical issues found. Please fix them.${NC}"
fi
echo "============================================"
echo ""
echo "Quick start:"
echo "  1. make setup      # First-time full setup"
echo "  2. make run         # Start controller"
echo "  3. make dev         # Start with live reload"
