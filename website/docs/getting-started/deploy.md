---
title: Deploying Your First App
---

## Creating a cluster

A cluster is the hosting container for your apps. It represents a Kubernetes cluster, and will make use of a configurable number of nodes.

```bash
% kon cluster create
```

This command walks you through the cluster creation process, there are a few decisions to make:

* [Network Topology](networking.md)
* Machine instance type
* Min & max size of the nodepool

For a test cluster, you can pick the defaults to get it quickly started. This will take a few minutes, Konstellation uses Terraform to configure the underlying VPC and the cluster.

If a Konstellation-compatible VPC is already available, it would be available as an option vs creating a new cluster. This is helpful when performing cluster migrations.

:::caution
This creates AWS resources that will incur costs that you will be responsible for.
:::

## Selecting an active cluster

Konstellation supports working with multiple clusters. You will need to choose an active one in order to interact with it, including managing apps on that cluster.

```text
% kon cluster select <yourcluster>
```

## Deploying your first app

First, create an app template with the CLI.

```text
% kon app new
```

Enter your docker image and then open the generated yaml file and edit it. There are a few things you'll need to change:

* registry - if you are not using DockerHub, enter url of your docker registry
* ingress.hosts - one or more domains that you'd like your app to handle

Once complete, then load your config into Kubernetes with

```
% kon app load <yourapp>.yaml
```

That's it! With that, Konstellation will create all of the resources necessary to run your app on Kubernetes. It creates a native load balancer and outputs its address.

The app manifest is persisted in Kubernetes, and can be edited at any time with `kon app edit <app>`

### Checking app status

The status command gives an overview of the state of your app as currently deployed. It's useful to check up on different releases of the app, and load balancer status.

```
% kon app status <yourapp>

Target: production
Hosts: your.host.com
Load Balancer: b0d94b2f-istiosystem-konin-a4cf-358886547.us-west-2.elb.amazonaws.com
Scale: 1 min, 5 max

RELEASE                       BUILD              DATE                    PODS    STATUS    TRAFFIC
yourapp-20200423-1531-7800    yourrepo/image     2020-05-16 23:01:23     1/1     released  100%
```

### Routing your domain

What remains is linking your domain to the load balancer. You'll need to create a CNAME record, and in the field, specify the `Load Balancer` address shown in the status output.

### Setting up SSL

On EKS, Konstellation uses an [Application Load Balancer (ALB)](https://aws.amazon.com/elasticloadbalancing/features/) for your ingress. ALB is a layer 7 load balancer and is capable of terminating SSL/TLS requests.

Because the termination is handled by ALB, Konstellation (or Kubernetes) does not need your certificate nor private key. This creates a secure setup since we no longer have to worry about securing Kubernetes' key storage. As long as your certificates are stored in ACM, Konstellation needs only a reference to them.

To use SSL with Konstellation, first ensure your certificate is uploaded into [ACM](https://console.aws.amazon.com/acm/home), then sync certificate references into Kubernetes with:

```text
% kon certificate sync
```

After this, your app should be automatically available via HTTPS. Note: ACM is region aware, your cluster and certificates must reside in the same region as the cluster for them to be usable.

### Configuring your app

For most non-trivial apps, you'd likely want to pass in configuration. Konstellation lets you manage both app and shared configs. See [Configuration](apps.md#Configuration) for details.

## Cleaning it all up

When Konstellation creates VPC and cluster resources, it keeps track of the state to make it possible to delete those resources at a later point.

### Deleting your cluster

Clusters are removed with the `cluster destroy` subcommand. Remove the cluster you've created with `kon cluster destroy --cluster <yourcluster>`.

This process could take up to 10-15 minutes. So just hang tight and grab a drink. :sunglasses:

### Destroy VPC & networking resources

If you no longer want the VPC, or want to change your network topology, it's simple to destroy it and start over.

First check the ID of the VPC:

```text
% kon vpc list

aws (us-west-2)
------------------------------------------------------------------------
  VPC                     CIDR BLOCK    KONSTELLATION   TOPOLOGY
------------------------------------------------------------------------
  vpc-00a63c9cb05d5320f   10.1.0.0/16   yes             public_private
------------------------------------------------------------------------
```

Then destroy it.

```text
% kon vpc destroy --vpc <yourvpc>
```