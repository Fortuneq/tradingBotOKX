# VPS Deployment Guide

This guide will help you deploy the OKX Trading Bot on a VPS (Ubuntu/Debian).

## Quick Deployment

1. **Upload files to VPS:**
   ```bash
   scp -r . user@your-vps-ip:/tmp/trading-bot
   ```

2. **Run deployment script:**
   ```bash
   ssh user@your-vps-ip
   cd /tmp/trading-bot
   sudo ./deploy.sh
   ```

3. **Configure API credentials:**
   ```bash
   sudo nano /opt/trading-bot/.env
   # Edit the OKX API credentials
   ```

4. **Restart the service:**
   ```bash
   sudo systemctl restart okx-trading-bot
   ```

## Manual Deployment

### Prerequisites

- Ubuntu 20.04+ or Debian 11+
- Root access or sudo privileges
- At least 1GB RAM and 10GB storage

### Step 1: Install Docker

```bash
# Update system
sudo apt update && sudo apt upgrade -y

# Install Docker
sudo apt install -y docker.io docker-compose

# Start Docker service
sudo systemctl start docker
sudo systemctl enable docker

# Add user to docker group
sudo usermod -aG docker $USER
```

### Step 2: Setup Application

```bash
# Create application directory
sudo mkdir -p /opt/trading-bot
cd /opt/trading-bot

# Copy your project files here
# Or clone from git repository

# Create environment file
sudo cp .env.example .env
sudo nano .env  # Edit with your OKX API credentials
```

### Step 3: Deploy

```bash
# Build and start containers
sudo docker-compose up -d --build

# Check status
sudo docker-compose ps
sudo docker-compose logs -f trading-bot
```

### Step 4: Setup Firewall

```bash
# Enable firewall
sudo ufw enable

# Allow necessary ports
sudo ufw allow ssh
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw allow 8080/tcp
```

### Step 5: Setup Auto-start Service

```bash
# Create systemd service
sudo tee /etc/systemd/system/okx-trading-bot.service > /dev/null <<EOF
[Unit]
Description=OKX Trading Bot
Requires=docker.service
After=docker.service

[Service]
Type=oneshot
RemainAfterExit=yes
WorkingDirectory=/opt/trading-bot
ExecStart=/usr/bin/docker-compose up -d
ExecStop=/usr/bin/docker-compose down
TimeoutStartSec=0

[Install]
WantedBy=multi-user.target
EOF

# Enable service
sudo systemctl daemon-reload
sudo systemctl enable okx-trading-bot
sudo systemctl start okx-trading-bot
```

## Configuration

### Environment Variables

Copy `.env.example` to `.env` and configure:

- **OKX__ApiKey**: Your OKX API key
- **OKX__SecretKey**: Your OKX secret key  
- **OKX__Passphrase**: Your OKX passphrase
- **OKX__Environment**: Set to `demo` for testing, `live` for production
- **Strategy parameters**: Adjust risk and trading parameters

### Important Security Notes

1. **Never commit `.env` file to version control**
2. **Use demo environment for testing**
3. **Start with small risk percentage (0.1-1%)**
4. **Monitor logs regularly**

## Management Commands

```bash
# Start bot
sudo systemctl start okx-trading-bot

# Stop bot
sudo systemctl stop okx-trading-bot

# Restart bot
sudo systemctl restart okx-trading-bot

# Check status
sudo systemctl status okx-trading-bot

# View logs
cd /opt/trading-bot
sudo docker-compose logs -f trading-bot

# Update bot
cd /opt/trading-bot
git pull  # if using git
sudo docker-compose up -d --build
```

## Monitoring

### Health Check

The bot exposes a health check endpoint:
```bash
curl http://localhost:8080/health
```

### Log Files

Logs are stored in `/opt/trading-bot/logs/` directory.

### Container Status

```bash
cd /opt/trading-bot
sudo docker-compose ps
```

## Troubleshooting

### Common Issues

1. **Container won't start:**
   ```bash
   sudo docker-compose logs trading-bot
   ```

2. **API connection issues:**
   - Check API credentials in `.env`
   - Verify OKX API permissions
   - Check network connectivity

3. **Permission errors:**
   ```bash
   sudo chown -R $USER:$USER /opt/trading-bot
   ```

4. **Port conflicts:**
   ```bash
   sudo netstat -tulpn | grep :8080
   ```

### Log Analysis

```bash
# Real-time logs
sudo docker-compose logs -f trading-bot

# Last 100 lines
sudo docker-compose logs --tail=100 trading-bot

# Search for errors
sudo docker-compose logs trading-bot | grep -i error
```

## Backup and Recovery

### Backup Configuration

```bash
# Backup configuration and logs
sudo tar -czf trading-bot-backup-$(date +%Y%m%d).tar.gz /opt/trading-bot/.env /opt/trading-bot/logs
```

### Restore Configuration

```bash
# Extract backup
sudo tar -xzf trading-bot-backup-YYYYMMDD.tar.gz -C /
sudo systemctl restart okx-trading-bot
```

## Security Recommendations

1. **Regular Updates:**
   ```bash
   sudo apt update && sudo apt upgrade -y
   ```

2. **Firewall Configuration:**
   - Only open necessary ports
   - Use fail2ban for SSH protection

3. **Monitoring:**
   - Set up log monitoring
   - Monitor resource usage
   - Set up alerts for trading errors

4. **Access Control:**
   - Use SSH keys instead of passwords
   - Limit sudo access
   - Regular security audits