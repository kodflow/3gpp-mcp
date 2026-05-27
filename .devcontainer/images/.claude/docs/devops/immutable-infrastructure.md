# Immutable Infrastructure

> Replace servers instead of modifying them.

**Principle:** Treat servers as cattle, not as pets.

## Principle

```
┌─────────────────────────────────────────────────────────────────┐
│                  MUTABLE vs IMMUTABLE                            │
│                                                                  │
│  MUTABLE (traditionnel)           IMMUTABLE (moderne)           │
│  ┌─────────────────────┐          ┌─────────────────────┐       │
│  │     Server v1       │          │     Server v1       │       │
│  │  ┌───────────────┐  │          │  (destroyed)        │       │
│  │  │  App v1.0     │  │          └─────────────────────┘       │
│  │  └───────────────┘  │                    ↓                   │
│  │         ↓           │          ┌─────────────────────┐       │
│  │  ┌───────────────┐  │          │     Server v2       │       │
│  │  │  + Patch      │  │          │  (new from image)   │       │
│  │  └───────────────┘  │          │  ┌───────────────┐  │       │
│  │         ↓           │          │  │  App v1.1     │  │       │
│  │  ┌───────────────┐  │          │  │  + All deps   │  │       │
│  │  │  + Config     │  │          │  └───────────────┘  │       │
│  │  │  + Hotfix     │  │          └─────────────────────┘       │
│  │  │  + Drift...   │  │                                        │
│  │  └───────────────┘  │                                        │
│  └─────────────────────┘                                        │
│                                                                  │
│  Problem: Configuration drift        Solution: Known state       │
└─────────────────────────────────────────────────────────────────┘
```

## Pipeline immutable

```
┌─────────────────────────────────────────────────────────────────┐
│                      BUILD PIPELINE                              │
│                                                                  │
│  ┌──────────┐    ┌──────────┐    ┌──────────┐    ┌──────────┐  │
│  │  Code    │───▶│  Build   │───▶│  Test    │───▶│  Image   │  │
│  │  Commit  │    │  Docker  │    │  Image   │    │  Registry│  │
│  └──────────┘    └──────────┘    └──────────┘    └──────────┘  │
│                                                       │         │
│  ┌────────────────────────────────────────────────────┘         │
│  │                                                               │
│  ▼                                                               │
│  ┌──────────┐    ┌──────────┐    ┌──────────┐                   │
│  │  Deploy  │───▶│  Health  │───▶│  Route   │                   │
│  │  New     │    │  Check   │    │  Traffic │                   │
│  │  Instance│    │          │    │          │                   │
│  └──────────┘    └──────────┘    └──────────┘                   │
│                                       │                          │
│                                       ▼                          │
│                              ┌──────────────┐                    │
│                              │   Destroy    │                    │
│                              │   Old        │                    │
│                              │   Instance   │                    │
│                              └──────────────┘                    │
└─────────────────────────────────────────────────────────────────┘
```

## Implementation with Packer + Terraform

### Packer - Image Creation

```hcl
# packer/app.pkr.hcl
packer {
  required_plugins {
    amazon = {
      version = ">= 1.0.0"
      source  = "github.com/hashicorp/amazon"
    }
  }
}

variable "app_version" {
  type = string
}

source "amazon-ebs" "app" {
  ami_name      = "myapp-${var.app_version}-{{timestamp}}"
  instance_type = "t3.micro"
  region        = "eu-west-1"

  source_ami_filter {
    filters = {
      name                = "ubuntu/images/*ubuntu-jammy-22.04-amd64-server-*"
      root-device-type    = "ebs"
      virtualization-type = "hvm"
    }
    owners      = ["099720109477"]  # Canonical
    most_recent = true
  }

  ssh_username = "ubuntu"

  tags = {
    Name        = "myapp"
    Version     = var.app_version
    Environment = "production"
  }
}

build {
  sources = ["source.amazon-ebs.app"]

  # Install dependencies
  provisioner "shell" {
    inline = [
      "sudo apt-get update",
      "sudo apt-get install -y docker.io",
      "sudo systemctl enable docker",
    ]
  }

  # Deploy application
  provisioner "shell" {
    inline = [
      "sudo docker pull myregistry/myapp:${var.app_version}",
      "sudo docker tag myregistry/myapp:${var.app_version} myapp:latest",
    ]
  }

  # Configure systemd
  provisioner "file" {
    source      = "files/myapp.service"
    destination = "/tmp/myapp.service"
  }

  provisioner "shell" {
    inline = [
      "sudo mv /tmp/myapp.service /etc/systemd/system/",
      "sudo systemctl enable myapp",
    ]
  }
}
```

### Terraform - Deployment

```hcl
# terraform/main.tf
variable "ami_id" {
  description = "AMI ID from Packer build"
  type        = string
}

resource "aws_launch_template" "app" {
  name_prefix   = "myapp-"
  image_id      = var.ami_id
  instance_type = "t3.medium"

  vpc_security_group_ids = [aws_security_group.app.id]

  user_data = base64encode(<<-EOF
    #!/bin/bash
    # Minimal config - app already in AMI
    echo "Starting pre-baked application..."
    systemctl start myapp
  EOF
  )

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_autoscaling_group" "app" {
  name                = "myapp-${var.ami_id}"
  desired_capacity    = 3
  min_size            = 2
  max_size            = 10
  vpc_zone_identifier = var.subnet_ids

  launch_template {
    id      = aws_launch_template.app.id
    version = "$Latest"
  }

  health_check_type         = "ELB"
  health_check_grace_period = 300

  instance_refresh {
    strategy = "Rolling"
    preferences {
      min_healthy_percentage = 75
    }
  }

  lifecycle {
    create_before_destroy = true
  }
}
```

## Docker: Immutable by Default

```dockerfile
# Dockerfile - Image immutable
FROM node:20-alpine AS builder
WORKDIR /app
COPY package*.json ./
RUN npm ci --only=production
COPY . .
RUN npm run build

FROM node:20-alpine AS runtime
WORKDIR /app

# Non-root user
RUN addgroup -S appgroup && adduser -S appuser -G appgroup
USER appuser

# Copy only what is needed
COPY --from=builder /app/dist ./dist
COPY --from=builder /app/node_modules ./node_modules

# Configuration via env vars, not files
ENV NODE_ENV=production
ENV PORT=8080

EXPOSE 8080
CMD ["node", "dist/main.js"]
```

## Externalized Configuration

```yaml
# kubernetes/configmap.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: myapp-config
data:
  LOG_LEVEL: "info"
  FEATURE_X: "enabled"
---
# kubernetes/secret.yaml
apiVersion: v1
kind: Secret
metadata:
  name: myapp-secrets
type: Opaque
data:
  DATABASE_URL: <base64-encoded>
---
# kubernetes/deployment.yaml
spec:
  containers:
  - name: app
    image: myapp:v1.2.3  # Image immutable
    envFrom:
    - configMapRef:
        name: myapp-config
    - secretRef:
        name: myapp-secrets
```

## When to Use

| Use | Avoid |
|-----|-------|
| Critical production | Prototypes/MVPs |
| Compliance required | Dev environment |
| Horizontal scale | Legacy applications |
| Cloud native | Constraining on-premise |
| Mature CI/CD | Team without automation |

## Advantages

- **Reproducibility**: Same image = same behavior
- **No drift**: No manual configuration
- **Easy rollback**: Redeploy old image
- **Scalability**: Identical instances
- **Audit**: Change traceability
- **Security**: Reduced attack surface

## Disadvantages

- **Build time**: Images to rebuild
- **Storage**: Multiple images
- **Cold start**: New instances
- **Logs/State**: Must be externalized
- **Initial complexity**: Pipeline to build

## Real-World Examples

| Company | Implementation |
|---------|----------------|
| **Netflix** | AMI baking, Spinnaker |
| **Google** | Borg, Kubernetes |
| **Spotify** | Docker partout |
| **Etsy** | Immutable deploys |
| **HashiCorp** | Packer + Terraform |

## Anti-patterns

| Anti-pattern | Problem | Solution |
|--------------|---------|----------|
| SSH in production | Manual modifications | Rebuild image |
| Local config | Configuration drift | ConfigMap/Secrets |
| Direct hotfix | Not reproducible | CI/CD pipeline |
| Local logs | Lost on destroy | ELK/CloudWatch |

## Migration path

### From Mutable Infrastructure

```
Phase 1: Containerize applications
Phase 2: Externalize configuration
Phase 3: Implement CI/CD pipeline
Phase 4: Infrastructure as Code
Phase 5: Eliminate SSH access to production
```

### Migration Checklist

- [ ] Applications containerized
- [ ] Configuration externalized (env vars)
- [ ] Logs to centralized service
- [ ] State to external storage (S3, DB)
- [ ] Automated build pipeline
- [ ] Tests on images
- [ ] Automated rollback

## Related Patterns

| Pattern | Relation |
|---------|----------|
| Blue-Green | Immutable image deployment |
| GitOps | Declarative management |
| Infrastructure as Code | Automated provisioning |
| Containerization | App-level immutability |

## Sources

- [HashiCorp Packer](https://www.packer.io/)
- [Martin Fowler - Phoenix Server](https://martinfowler.com/bliki/PhoenixServer.html)
- [Netflix Tech Blog](https://netflixtechblog.com/)
- [12 Factor App](https://12factor.net/)
