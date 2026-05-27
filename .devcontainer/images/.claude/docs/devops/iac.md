# Infrastructure as Code (IaC)

> Manage infrastructure through versioned code.

## Principle

```
┌─────────────────────────────────────────────────────────────────┐
│                         CODE (Git)                               │
│                                                                  │
│  main.tf                                                         │
│  ┌─────────────────────────────────────────────────────────────┐│
│  │ resource "aws_instance" "web" {                             ││
│  │   ami           = "ami-12345"                               ││
│  │   instance_type = "t3.micro"                                ││
│  │ }                                                           ││
│  └─────────────────────────────────────────────────────────────┘│
└─────────────────────────────────────┬───────────────────────────┘
                                      │
                                      │ terraform apply
                                      ▼
┌─────────────────────────────────────────────────────────────────┐
│                      CLOUD PROVIDER                              │
│                                                                  │
│  ┌─────────┐    ┌─────────┐    ┌─────────┐                     │
│  │   EC2   │    │   RDS   │    │   S3    │                     │
│  └─────────┘    └─────────┘    └─────────┘                     │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

## Tools

| Tool | Type | Provider |
|------|------|----------|
| **Terraform** | Declarative | Multi-cloud |
| **OpenTofu** | Declarative | Multi-cloud (OSS fork) |
| **Pulumi** | Imperative | Multi-cloud |
| **CloudFormation** | Declarative | AWS only |
| **ARM/Bicep** | Declarative | Azure only |
| **Ansible** | Configuration | Multi-platform |

## Structure Terraform

```
infrastructure/
├── modules/                    # Reusable modules
│   ├── vpc/
│   │   ├── main.tf
│   │   ├── variables.tf
│   │   └── outputs.tf
│   ├── eks/
│   └── rds/
│
├── environments/               # Per environment
│   ├── dev/
│   │   ├── main.tf
│   │   ├── terraform.tfvars
│   │   └── backend.tf
│   ├── staging/
│   └── prod/
│
└── global/                     # Shared resources
    ├── iam/
    └── dns/
```

## Terraform Example

```hcl
# modules/vpc/main.tf
resource "aws_vpc" "main" {
  cidr_block = var.cidr_block

  tags = {
    Name        = var.name
    Environment = var.environment
  }
}

resource "aws_subnet" "public" {
  count             = length(var.availability_zones)
  vpc_id            = aws_vpc.main.id
  cidr_block        = cidrsubnet(var.cidr_block, 8, count.index)
  availability_zone = var.availability_zones[count.index]

  tags = {
    Name = "${var.name}-public-${count.index}"
  }
}

# environments/prod/main.tf
module "vpc" {
  source = "../../modules/vpc"

  name               = "production"
  environment        = "prod"
  cidr_block         = "10.0.0.0/16"
  availability_zones = ["eu-west-1a", "eu-west-1b", "eu-west-1c"]
}
```

## Workflow

```
1. terraform init      # Initialize
2. terraform plan      # Preview
3. terraform apply     # Apply
4. terraform destroy   # Destroy (caution!)
```

## Best Practices

### 1. State Management

```hcl
# backend.tf - Remote state
terraform {
  backend "s3" {
    bucket         = "my-terraform-state"
    key            = "prod/terraform.tfstate"
    region         = "eu-west-1"
    encrypt        = true
    dynamodb_table = "terraform-locks"
  }
}
```

### 2. Variables & Secrets

```hcl
# NEVER commit secrets
# Use environment variables or Vault

variable "db_password" {
  type      = string
  sensitive = true
}

# Via environment variable
# export TF_VAR_db_password="secret"
```

### 3. Modules

```hcl
# Reuse via modules
module "web_server" {
  source = "./modules/ec2"

  instance_type = "t3.medium"
  ami           = data.aws_ami.ubuntu.id
}
```

### 4. Validation

```yaml
# CI Pipeline
- terraform fmt -check
- terraform validate
- terraform plan
- tflint
- checkov --directory .
```

## Immutable vs Mutable

| Approach | Description | Tool |
|----------|-------------|------|
| **Immutable** | Replace, do not modify | Terraform, Packer |
| **Mutable** | Modify in place | Ansible, Chef |

```
Immutable (recommended):
┌─────────┐     ┌─────────┐
│Server v1│ ──▶ │Server v2│  (new server)
└─────────┘     └─────────┘

Mutable:
┌─────────┐     ┌─────────┐
│Server v1│ ──▶ │Server v1│  (same server modified)
└─────────┘     │  + pkg  │
                └─────────┘
```

## Related Patterns

| Pattern | Relation |
|---------|----------|
| GitOps | IaC in Git |
| Immutable Infrastructure | Servers replaced |
| Blue-Green | Two IaC environments |

## Sources

- [Terraform Docs](https://developer.hashicorp.com/terraform)
- [Gruntwork IaC Guide](https://gruntwork.io/guides/)
