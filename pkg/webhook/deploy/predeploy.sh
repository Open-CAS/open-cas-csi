#!/bin/sh

# Exit on error.
set -e

usage() {
    echo 'Usage: predeploy.sh <serviceName> <namespace> <secretName>'
}

if [ "$#" -ne 3 ]; then
    usage
    exit 1
fi

service=$1
namespace=$2
secret=$3

# Create namespace if not exists.
kubectl create namespace $namespace || true
kubectl label namespace $namespace csi.open-cas.com/protected=

# Locate the cluster Certificate Authority for population in webhook YAML.
CA_BUNDLE=`kubectl get configmap -n kube-system extension-apiserver-authentication -o=jsonpath='{.data.client-ca-file}' | base64 | tr -d '\n'`

# Check if secret already exists.
set +e
out=`kubectl get secret -n $namespace $secret 2>&1`
if [ "$?" -ne 0 ]; then
    echo SECRET: $out
    set -e
    # Verify that error is 'not found'.
    echo $out | grep "not found"
    # Populate secrets from certificate file and key.
    ./generate-secret.sh $service $namespace $secret
fi

# Replace static strings with variables and deploy.
set +e
out=`sed -e "s/CA_BUNDLE/$CA_BUNDLE/g" -e "s/SERVICE/$service/g" -e "s/NAMESPACE/$namespace/g" <webhook.yaml |
  kubectl create -f - 2>&1`
if [ "$?" -ne 0 ]; then
    set -e
    # Verify that error is 'already exists'.
    echo $out | grep "already exists"
    exit
fi
