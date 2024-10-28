#!/bin/bash

# List of PARAMS that can be set in command line with their default values (if set):
#
# KUBECTL=kubectl
# KUBELET_DIR=/var/lib/kubelet
# SELECTOR='node-role.kubernetes.io/worker: ""'     A label by which kernel module and driver placement will be defined.
# CONTAINER_TOOL=docker
# CERT_MANAGER_VER=v1.13.3
# VERSION=latest                                    A version tag for output Open-CAS CSI images
# REGISTRY=                                         A registry where images built with this script can be stored
# ALLOW_INSECURE=0                                  Allow access to registry through HTTP
# OPENCAS_VERSION=                                  Tag/branch of the Open-CAS repo to use
# REDEPLOY=0                                        Rebuild images and update OpenCAS-CSI deployments
# UBUNTU_IMAGE=ubuntu                               Custom (local?) Ubuntu image location
# CA_CERTS_IMAGE=                                   If CA-certs are needed, place them in a container image


echo -e "\nStarting installation script for Open-CAS CSI..."

base_dir=$PWD

if [[ -z $KUBECTL ]]; then
    KUBECTL=kubectl
fi

# PRE-REQUISITES

## cert-manager
lines=$(($($KUBECTL get deployments -n cert-manager --no-headers | wc -l)))
if [[ $lines -ge 3 ]]; then
    echo -e "\ncert-manager deployment found, skipping"
else
    if [[ -z $CERT_MANAGER_VER ]]; then
        CERT_MANAGER_VER=v1.13.3
    fi
    echo -e "\nInstalling cert-manager ${CERT_MANAGER_VER}..."
    $KUBECTL apply -f https://github.com/cert-manager/cert-manager/releases/download/$CERT_MANAGER_VER/cert-manager.yaml
fi

$KUBECTL -n cert-manager wait --for=condition=Available deployment \
    cert-manager \
    cert-manager-cainjector \
    cert-manager-webhook
code=$?
if [[ $code -ne 0 ]]; then
    echo "cert-manager not ready yet, rerun script later"
    exit $code
fi

echo "cert-manager ready"
## --END cert-manager

## KMMO v1.1
lines=$(($($KUBECTL get deployments kmm-operator-controller-manager -n kmm-operator-system --no-headers | wc -l)))
if [[ $lines -eq 1 ]]; then
    echo -e "\nKMM Operator deployment found, skipping"
else
    echo -e "\nInstalling KMM Operator v1.1..."
    temp_dir=$(mktemp -d)
    cd $temp_dir
    git clone https://github.com/kubernetes-sigs/kernel-module-management.git
    cd kernel-module-management
    git checkout release-1.1
    sed -i "s@kubectl @${KUBECTL} @g" Makefile
    sed -i "s@docker @${CONTAINER_TOOL} @g" Makefile
    sed -i "s@deploy: deploy-cert-manager@deploy:@g" Makefile # skip cert-manager (already installed above)
    make deploy IMAGE_TAG=v20230606-v1.1.0
    cd $base_dir
    rm -rf $temp_dir
fi

$KUBECTL -n kmm-operator-system wait --for=condition=Available deployment kmm-operator-controller-manager
code=$?
if [[ $code -ne 0 ]]; then
    echo "KMM Operator not ready yet, rerun script later"
    exit $code
fi

echo "KMM Operator ready"
## --END KMMO v1.1

# --END PRE-REQUISITES

# Open-CAS kernel module deployment
if [[ -z $OPENCAS_VERSION ]]; then
    echo -e "\nOpen-CAS version not provided - provide it by setting OPENCAS_VERSION="
    exit 1
fi

if [[ -z $REGISTRY ]]; then
    echo -e "\nRegistry for storing container images not provided - provide it by setting REGISTRY="
    exit 1
fi

if [[ -z "$SELECTOR" ]]; then
    SELECTOR='node-role.kubernetes.io/worker: ""'
fi

if [[ -n "$CA_CERTS_IMAGE" ]]; then
    FROM_CA_CERTS="FROM ${CA_CERTS_IMAGE} as certs"
    COPY_CA_CERTS='COPY --from=certs /usr/local/share/ca-certificates /usr/local/share/ca-certificates'
    INSTALL_CA_CERTS='RUN apt-get install -y ca-certificates openssl \&\& update-ca-certificates'
fi

lines=$(($($KUBECTL get module -n opencas-module --no-headers | wc -l)))
if [[ $REDEPLOY -eq false && $lines -ge 1 ]]; then
    echo -e "\nOpen-CAS Module resource found, skipping creation"
else
    echo -e "\nCreating Open-CAS Module resources for KMMO..."
    module=$(sed "s@OPENCAS_VERSION@${OPENCAS_VERSION}@g" <kmmo-opencas-module.yaml \
            | sed "s@HTTP_PROXY@${http_proxy}@g" \
            | sed "s@HTTPS_PROXY@${https_proxy}@g" \
            | sed "s@REGISTRY@${REGISTRY}@g" \
            | sed "s@SELECTOR@${SELECTOR}@g")
    if [[ -n $UBUNTU_IMAGE ]]; then
        module=$(echo "$module" | sed "s@ubuntu@${UBUNTU_IMAGE}@g")
    fi
    if [[ $ALLOW_INSECURE -eq true ]]; then
        module=$(echo "$module" | sed "s@insecure: false@insecure: true@g" \
                | sed "s@insecureSkipTLSVerify: false@insecureSkipTLSVerify: true@g")
    fi
    if [[ -n "$CA_CERTS_IMAGE" ]]; then
        module=$(echo "$module" | sed "s@#FROM_CA_CERTS@${FROM_CA_CERTS}@" | sed "s@#COPY_CA_CERTS@${COPY_CA_CERTS}@" \
                | sed "s@#INSTALL_CA_CERTS@${INSTALL_CA_CERTS}@")
    fi
    echo "$module" | $KUBECTL apply -f -
fi
# --END Open-CAS kernel module deployment

# Open-CAS CSI images preparation
set -e  # break on error
echo -e "\nPreparing images..."
if [[ -z $CONTAINER_TOOL ]]; then
    CONTAINER_TOOL=docker
fi
if [[ -z $VERSION ]]; then
    VERSION=latest
fi

## casadm binary image
CASADM_IMG="${REGISTRY}/casadm:${OPENCAS_VERSION}"
if [[ $REDEPLOY -eq false && -n "$(${CONTAINER_TOOL} images -q ${CASADM_IMG} 2>/dev/null)" ]]; then
    echo -e "\ncasadm binary image ${CASADM_IMG} found, skipping"
else
    echo -e "\nPreparing casadm binary image..."
    dockerfile=$(cat Dockerfile.casadm)
    if [[ -n $UBUNTU_IMAGE ]]; then
        dockerfile=$(echo "$dockerfile" | sed "s@ubuntu@${UBUNTU_IMAGE}@g")
    fi
    if [[ -n "$CA_CERTS_IMAGE" ]]; then
        dockerfile=$(echo "$dockerfile" | sed "s@#FROM_CA_CERTS@${FROM_CA_CERTS}@" | sed "s@#COPY_CA_CERTS@${COPY_CA_CERTS}@" \
                    | sed "s@#INSTALL_CA_CERTS@${INSTALL_CA_CERTS}@")
    fi
    echo "$dockerfile" | $CONTAINER_TOOL build -t $CASADM_IMG --build-arg http_proxy=${http_proxy} \
        --build-arg https_proxy=${https_proxy} --build-arg OPENCAS_VERSION=${OPENCAS_VERSION} . -f -
    $CONTAINER_TOOL push $CASADM_IMG
fi
## --END casadm binary image

## Open-CAS CSI Driver image
DRIVER_IMG="${REGISTRY}/opencas-csi-driver:${VERSION}"
if [[ $REDEPLOY -eq false && -n "$(${CONTAINER_TOOL} images -q ${DRIVER_IMG} 2>/dev/null)" ]]; then
    echo -e "\nOpen-CAS CSI Driver image ${DRIVER_IMG} found, skipping"
else
    echo -e "\nPreparing Open-CAS CSI Driver image..."
    cd ../pkg/csi-driver/; CA_CERTS_IMAGE=$CA_CERTS_IMAGE IMG=$DRIVER_IMG CONTAINER_TOOL=$CONTAINER_TOOL make docker-build docker-push
    cd $base_dir
fi
## --END Open-CAS CSI Driver image

## Open-CAS CSI Operator image
OPERATOR_IMG="${REGISTRY}/opencas-csi-operator:${VERSION}"
if [[ $REDEPLOY -eq false && -n "$(${CONTAINER_TOOL} images -q ${OPERATOR_IMG} 2>/dev/null)" ]]; then
    echo -e "\nOpen-CAS CSI Operator image ${OPERATOR_IMG} found, skipping"
else
    echo -e "\nPreparing Open-CAS CSI Operator image..."
    cd ../pkg/csi-operator/; CA_CERTS_IMAGE=$CA_CERTS_IMAGE IMG=$OPERATOR_IMG CONTAINER_TOOL=$CONTAINER_TOOL make docker-build docker-push
    cd $base_dir
fi
## --END Open-CAS CSI Operator image

## Open-CAS CSI Webhook server image
WEBHOOK_IMG="${REGISTRY}/opencas-webhook:${VERSION}"
if [[ $REDEPLOY -eq false && -n "$(${CONTAINER_TOOL} images -q ${WEBHOOK_IMG} 2>/dev/null)" ]]; then
    echo -e "\nOpen-CAS CSI Webhook server image ${WEBHOOK_IMG} found, skipping"
else
    echo -e "\nPreparing Open-CAS CSI Webhook server image..."
    dockerfile=$(cat ../Dockerfile.webhookserver)
    if [[ -n "$CA_CERTS_IMAGE" ]]; then
        dockerfile=$(echo "$dockerfile" | sed "s@#FROM_CA_CERTS@${FROM_CA_CERTS}@" | sed "s@#COPY_CA_CERTS@${COPY_CA_CERTS}@" \
                    | sed "s@#INSTALL_CA_CERTS@${INSTALL_CA_CERTS}@")
    fi
    echo "$dockerfile" | $CONTAINER_TOOL build --build-arg http_proxy=${http_proxy} --build-arg https_proxy=${https_proxy} \
        --build-arg GOPROXY=`go env GOPROXY` -t $WEBHOOK_IMG -f - ..
    $CONTAINER_TOOL push $WEBHOOK_IMG
fi
## --END Open-CAS CSI Webhook server image

# --END Open-CAS CSI images preparation

# Open-CAS CSI Operator deployment
if [[ -z $KUBELET_DIR ]]; then
    KUBELET_DIR=/var/lib/kubelet
fi

lines=$(($($KUBECTL get deployments opencas-csi-operator-controller-manager -n opencas-csi-operator-system --no-headers | wc -l)))
if [[ $REDEPLOY -eq false && $lines -eq 1 ]]; then
    echo -e "\nOpen-CAS CSI Operator deployment found, skipping"
else
    echo -e "\nDeploying Open-CAS CSI Operator..."
    config_map_file="../pkg/csi-operator/config/manager/.env"
    echo "CASADM_IMAGE=${CASADM_IMG}" > $config_map_file
    echo "KUBELET_DIR=${KUBELET_DIR}" >> $config_map_file

    if [[ $REDEPLOY -eq 1 ]]; then
        cd ../pkg/csi-operator/; IMG=$OPERATOR_IMG make ignore-not-found=true undeploy
        cd $base_dir
    fi

    cd ../pkg/csi-operator/; IMG=$OPERATOR_IMG make deploy
    cd $base_dir
fi

$KUBECTL -n opencas-csi-operator-system wait --for=condition=Available deployment opencas-csi-operator-controller-manager
code=$?
if [[ $code -ne 0 ]]; then
    echo "Open-CAS CSI Operator not ready yet, rerun script later"
    exit $code
fi
# --END Open-CAS CSI Operator deployment

# Open-CAS CSI Driver deployment
echo -e "\nDeploying Open-CAS CSI Driver..."
driver=$(sed "s@CASADM_IMAGE@${CASADM_IMG}@g" <opencas-csi_driverinstance.yaml \
        | sed "s@DRIVER_IMAGE@${DRIVER_IMG}@g" \
        | sed "s@WEBHOOK_IMAGE@${WEBHOOK_IMG}@g" \
        | sed "s@#SELECTOR@${SELECTOR}@g")

if [[ $REDEPLOY -eq false ]]; then   
    echo "$driver" | $KUBECTL create -f -   # error if already exists
else
    echo "$driver" | $KUBECTL apply -f -    # create or modify existing
fi
# --END Open-CAS CSI Driver deployment
