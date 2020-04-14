provider "aws" {
  profile    = "default"
  region     = var.region
  version = "~> 2.57"
}

terraform {
  backend "s3" {
    bucket = "konstellation"
    key    = "terraform/aws/$${region}/$${cluster}/cluster.tfstate"
    region = "us-west-2"
  }
}
