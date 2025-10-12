#!/bin/bash

# Trading Bot OKX - VPS Deployment Script
set -e

echo "🚀 Starting OKX Trading Bot deployment..."

# Configuration
APP_DIR="/opt/trading-bot"
SERVICE_NAME="okx-trading-bot"
GIT_REPO="your-repo-url-here"  # Replace with your actual repository URL

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Function to print colored output
print_status() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Check if running as root
if [ "$EUID" -ne 0 ]; then
    print_error "Please run as root (use sudo)"
    exit 1
fi

# Update system packages
print_status "Updating system packages..."
apt update && apt upgrade -y

# Install required packages
print_status "Installing required packages..."
apt install -y docker.io docker-compose git curl wget nano ufw

# Start and enable Docker
print_status "Starting Docker service..."
systemctl start docker
systemctl enable docker

# Add current user to docker group (if not root)
if [ -n "$SUDO_USER" ]; then
    usermod -aG docker $SUDO_USER
    print_status "Added $SUDO_USER to docker group"
fi

# Configure firewall
print_status "Configuring firewall..."
ufw --force enable
ufw allow ssh
ufw allow 80/tcp
ufw allow 443/tcp
ufw allow 8080/tcp

# Create application directory
print_status "Creating application directory..."
mkdir -p $APP_DIR
cd $APP_DIR

# Clone or update repository
if [ ! -d ".git" ]; then
    print_status "Cloning repository..."
    if [ "$GIT_REPO" = "your-repo-url-here" ]; then
        print_warning "Please update the GIT_REPO variable in this script with your actual repository URL"
        print_status "Creating placeholder files for manual deployment..."
        # Create minimal structure for manual file upload
        mkdir -p logs data ssl
        chmod 755 logs data
    else
        git clone $GIT_REPO .
    fi
else
    print_status "Updating repository..."
    git pull origin main
fi

# Create environment file if it doesn't exist
if [ ! -f ".env" ]; then
    print_status "Creating environment configuration..."
    cat > .env << 'EOF'
# OKX API Configuration
OKX__ApiKey=your-api-key-here
OKX__SecretKey=your-secret-key-here
OKX__Passphrase=your-passphrase-here
OKX__Environment=live
OKX__InstrumentId=SOL-USDT

# Strategy Configuration
Strategy__RiskPercentage=1.0
Strategy__MaxPositions=1
Strategy__RSIPeriod=2
Strategy__VWAPPeriod=20

# Application Settings
ASPNETCORE_ENVIRONMENT=Production
ASPNETCORE_URLS=http://+:80

# Logging
Logging__LogLevel__Default=Information
Logging__LogLevel__Microsoft=Warning
EOF
    print_warning "Please update the .env file with your actual OKX API credentials!"
fi

# Create nginx configuration
print_status "Creating nginx configuration..."
cat > nginx.conf << 'EOF'
events {
    worker_connections 1024;
}

http {
    upstream trading-bot {
        server trading-bot:80;
    }

    server {
        listen 80;
        server_name _;

        location / {
            proxy_pass http://trading-bot;
            proxy_set_header Host $host;
            proxy_set_header X-Real-IP $remote_addr;
            proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
            proxy_set_header X-Forwarded-Proto $scheme;
        }

        location /health {
            proxy_pass http://trading-bot/health;
        }
    }
}
EOF

# Create directories with proper permissions
mkdir -p logs data ssl
chmod 755 logs data ssl

# Create systemd service for auto-start
print_status "Creating systemd service..."
cat > /etc/systemd/system/$SERVICE_NAME.service << EOF
[Unit]
Description=OKX Trading Bot
Requires=docker.service
After=docker.service

[Service]
Type=oneshot
RemainAfterExit=yes
WorkingDirectory=$APP_DIR
ExecStart=/usr/bin/docker-compose up -d
ExecStop=/usr/bin/docker-compose down
TimeoutStartSec=0

[Install]
WantedBy=multi-user.target
EOF

# Enable the service
systemctl daemon-reload
systemctl enable $SERVICE_NAME

# Build and start the application
print_status "Building and starting the trading bot..."
docker-compose down --remove-orphans 2>/dev/null || true
docker-compose up -d --build

# Wait for containers to be ready
print_status "Waiting for containers to start..."
sleep 30

# Check container status
print_status "Checking container status..."
docker-compose ps

# Display final information
print_status "Deployment completed successfully!"
echo ""
echo "📊 Trading Bot Status:"
echo "   - Application Directory: $APP_DIR"
echo "   - Service Name: $SERVICE_NAME"
echo "   - Web Interface: http://$(hostname -I | awk '{print $1}'):8080"
echo "   - Logs: docker-compose logs -f trading-bot"
echo ""
echo "🔧 Next Steps:"
echo "   1. Edit $APP_DIR/.env with your OKX API credentials"
echo "   2. Restart the service: systemctl restart $SERVICE_NAME"
echo "   3. Monitor logs: docker-compose logs -f"
echo ""
echo "📋 Useful Commands:"
echo "   - Start: systemctl start $SERVICE_NAME"
echo "   - Stop: systemctl stop $SERVICE_NAME"
echo "   - Status: systemctl status $SERVICE_NAME"
echo "   - Logs: cd $APP_DIR && docker-compose logs -f"
echo "   - Update: cd $APP_DIR && git pull && docker-compose up -d --build"

print_warning "Remember to configure your OKX API credentials in the .env file!"