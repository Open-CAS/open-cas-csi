#!/bin/sh

# Exit on error.
set -e

usage() {
    echo 'Usage: deploy.sh <serviceName> <namespace> <secretName> <serverImage>'
}

if [ "$#" -ne 4 ]; then
    usage
    exit 1
fi

service=$1
namespace=$2
secret=$3
image=$4

# Replace static strings with variables.
sed -e "s/SERVICE/$service/g" -e "s/NAMESPACE/$namespace/g" -e "s/SECRET/$secret/g" -e "s#IMAGE#$image#g" <server.yaml |
  kubectl create -f -
