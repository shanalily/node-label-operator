#!/bin/bash

# This is a sample script of commands necessary for setting up an aks cluster with the node-label-operator running.
# If using this to create your cluster, it is recommeneded that you run each step individually instead of trying to run the entire script.
# make sure to have $E2E_SUBSCRIPTION_ID set to the ID of your desired subscription and
# $DOCKERHUB_USER set to your dockerhub username

set -e
set -o pipefail

export AKS_NAME=node-label-test-aks
export AKS_RESOURCE_GROUP=${AKS_NAME}-rg
export MC_RESOURCE_GROUP=MC_${AKS_RESOURCE_GROUP}_${AKS_NAME}-cluster_westus2

# mkdir ~/aks 
export AZURE_AUTH_LOCATION=${PWD}/tests/aks/${AKS_NAME}-creds.json
export AZURE_IDENTITY_LOCATION=${PWD}/tests/aks/${AKS_NAME}-identity.json

az ad sp create-for-rbac --skip-assignment > $AZURE_AUTH_LOCATION

export AKS_CLIENT_ID=$(cat ${AZURE_AUTH_LOCATION} | jq -r .appId)
export AKS_CLIENT_SECRET=$(cat ${AZURE_AUTH_LOCATION} | jq -r .password)

az group create --name $AKS_RESOURCE_GROUP --location westus2

az aks create \
    --resource-group $AKS_RESOURCE_GROUP \
    --name ${AKS_NAME}-cluster \
    --node-count 3 \
    --service-principal $AKS_CLIENT_ID \
    --client-secret $AKS_CLIENT_SECRET \
    --generate-ssh-keys

az aks get-credentials --resource-group $AKS_RESOURCE_GROUP --name ${AKS_NAME}-cluster

export KUBECONFIG="$HOME/.kube/config"

az identity create -g $MC_RESOURCE_GROUP -n ${AKS_NAME}-identity -o json > $AZURE_IDENTITY_LOCATION
if [ $? -eq 0 ]; then
    echo "Created identity for resource group ${MC_RESOURCE_GROUP}, stored in ${AZURE_IDENTITY_LOCATION}"
else
    echo "Creating identity for resource group ${MC_RESOURCE_GROUP} failed"
fi

export RESOURCE_ID=$(cat ${AZURE_IDENTITY_LOCATION} | jq -r .id)
export CLIENT_ID=$(cat ${AZURE_IDENTITY_LOCATION} | jq -r .clientId)
export PRINCIPAL_ID=$(cat ${AZURE_IDENTITY_LOCATION} | jq -r .principalId)

# there seems to need to be some time between creating the identity and running the role commands 

az role assignment create --role "Managed Identity Operator" --assignee $PRINCIPAL_ID --scope $RESOURCE_ID
az role assignment create --role "Contributor" --assignee $PRINCIPAL_ID --scope /subscriptions/${E2E_SUBSCRIPTION_ID}/resourceGroups/${MC_RESOURCE_GROUP}

# create aadpodidentity.yaml in order to create AzureIdentity
sed 's/<subid>/'"${E2E_SUBSCRIPTION_ID}"'/g' samples/aadpodidentity.yaml | \
    sed 's/<resource-group>/'"${MC_RESOURCE_GROUP}"'/g' | \
    sed 's/<a-idname>/'"${AKS_NAME}"'-identity/g' | \
    sed 's/<name>/'"${AKS_NAME}"'-identity/g' | \
    sed 's/<clientId>/'"${CLIENT_ID}"'/g' \
    > tests/aks/aadpodidentity.yaml
if [ $? -eq 0 ]; then
    echo "Generated aadpodidentity.yaml file"
else
    echo "Failed to generate aadpodidentity.yaml file"
fi

# create aadpodidentitybinding.yaml in order to create AzureIdentityBinding
sed 's/<binding-name>/'"${AKS_NAME}"'-identity-binding/g' samples/aadpodidentitybinding.yaml | \
    sed 's/<identity-name>/'"${AKS_NAME}"'-identity/g' | \
    sed 's/<selector-name>/node-label-operator/g' \
    > tests/aks/aadpodidentitybinding.yaml
if [ $? -eq 0 ]; then
    echo "Generated aadpodidentitybinding.yaml file"
else
    echo "Failed to generate aadpodidentitybinding.yaml file"
fi

# apply aad pod identity stuff 
kubectl apply -f https://raw.githubusercontent.com/Azure/aad-pod-identity/master/deploy/infra/deployment-rbac.yaml
kubectl apply -f tests/aks/aadpodidentity.yaml
kubectl apply -f tests/aks/aadpodidentitybinding.yaml

# deploy controller 
export IMG="$DOCKERHUB_USER/node-label" # change to your dockerhub username
make docker-build docker-push
make deploy
kubectl apply -f samples/configmap.yaml
