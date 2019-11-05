#!/bin/bash

# This is a sample script of commands necessary for setting up an aks-engine cluster with the node-label-operator running.
# If using this to create your cluster, it is recommeneded that you run each step individually instead of trying to run the entire script.
# make sure to have $E2E_SUBSCRIPTION_ID set to the ID of your desired subscription and
# $DOCKERHUB_USER set to your dockerhub username

# exit if unsuccessful at any step
set -e
set -o pipefail

export AKS_ENGINE_NAME=node-label-test-akse
export AKS_ENGINE_RG=${AKS_ENGINE_NAME}-rg
export AZURE_AUTH_LOCATION=${PWD}/tests/aks-engine/creds.json
export AZURE_IDENTITY_LOCATION=${PWD}/tests/aks-engine/identity.json

az group create --name $AKS_ENGINE_RG --location westus2 

az ad sp create-for-rbac --role="Contributor" --scopes="/subscriptions/${E2E_SUBSCRIPTION_ID}/resourceGroups/${AKS_ENGINE_RG}" > $AZURE_AUTH_LOCATION
if [ $? -eq 0 ]; then
    echo "Created Contributor role for resource group ${AKS_ENGINE_RG}"
else
    echo "Creating Contributor role for resource group ${AKS_ENGINE_RG} failed"
fi

export AKS_ENGINE_CLIENT_ID=$(cat ${AZURE_AUTH_LOCATION} | jq -r .appId)
export AKS_ENGINE_CLIENT_SECRET=$(cat ${AZURE_AUTH_LOCATION} | jq -r .password)

# deploy aks-engine cluster
if [ -d "${PWD}/tests/aks-engine/_output/${AKS_ENGINE_NAME}-cluster" ]; then
    rm -rf ${PWD}/tests/aks-engine/_output/${AKS_ENGINE_NAME}-cluster
fi
aks-engine deploy --subscription-id $E2E_SUBSCRIPTION_ID \
    --resource-group $AKS_ENGINE_RG \
    --location westus2 \
    --dns-prefix ${AKS_ENGINE_NAME}-cluster \
    --api-model tests/aks-engine/kubernetes.json \
    --output-directory "${PWD}/tests/aks-engine/_output/${AKS_ENGINE_NAME}-cluster" \
    --client-id $AKS_ENGINE_CLIENT_ID \
    --client-secret $AKS_ENGINE_CLIENT_SECRET \
    --set servicePrincipalProfile.clientId="${AKS_ENGINE_CLIENT_ID}" \
    --set servicePrincipalProfile.secret="${AKS_ENGINE_CLIENT_SECRET}"

export KUBECONFIG="${PWD}/tests/aks-engine/_output/${AKS_ENGINE_NAME}-cluster/kubeconfig/kubeconfig.westus2.json"

# create MSI
az identity create -g $AKS_ENGINE_RG -n ${AKS_ENGINE_NAME}-identity -o json > $AZURE_IDENTITY_LOCATION
if [ $? -eq 0 ]; then
    echo "Created identity for resource group ${AKS_ENGINE_RG}, stored in ${AZURE_IDENTITY_LOCATION}"
else
    echo "Creating identity for resource group ${AKS_ENGINE_RG} failed"
fi

export RESOURCE_ID=$(cat ${AZURE_IDENTITY_LOCATION} | jq -r .id)
export CLIENT_ID=$(cat ${AZURE_IDENTITY_LOCATION} | jq -r .clientId)
export PRINCIPAL_ID=$(cat ${AZURE_IDENTITY_LOCATION} | jq -r .principalId)

# create roles
az role assignment create --role "Managed Identity Operator" --assignee $PRINCIPAL_ID --scope $RESOURCE_ID 
az role assignment create --role "Contributor" --assignee $PRINCIPAL_ID --scope /subscriptions/${E2E_SUBSCRIPTION_ID}/resourceGroups/${AKS_ENGINE_RG}

# create aad-pod-identity resources, including AzureIdentity and AzureIdentityBinding
kubectl apply -f https://raw.githubusercontent.com/Azure/aad-pod-identity/master/deploy/infra/deployment-rbac.yaml
cat tests/aks/aadpodidentity-config.yaml | envsubst | kubectl apply -f -

# deploy controller 
export IMG="shanalily/node-label" # change to your dockerhub username
make docker-build docker-push
make deploy
kubectl apply -f config/samples/configmap.yaml
