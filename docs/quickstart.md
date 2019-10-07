Getting Started

1. Assume you already have a cluster.

You have created a cluster using either AKS or aks-engine.

2. Create a user-assigned managed service identity if you don't have one.

```sh
export AZURE_RESOURCE_GROUP=
export AZURE_IDENTITY_LOCATION=~/identity.json
export AZURE_IDENTITY=

az identity create -g $AZURE_RESOURCE_GROUP -n ${AZURE_IDENTITY} -o json > $AZURE_IDENTITY_LOCATION

export AZURE_IDENTITY_RESOURCE_ID=$(cat ${AZURE_IDENTITY_LOCATION} | jq -r .id)
export AZURE_IDENTITY_CLIENT_ID=$(cat ${AZURE_IDENTITY_LOCATION} | jq -r .clientId)
export AZURE_IDENTITY_PRINCIPAL_ID=$(cat ${AZURE_IDENTITY_LOCATION} | jq -r .principalId)
```

3. Create roles for identity.

```sh
az role assignment create --role "Managed Identity Operator" --assignee $AZURE_IDENTITY_PRINCIPAL_ID --scope $AZURE_IDENTITY_RESOURCE_ID
az role assignment create --role "Contributor" --assignee $PRINCIPAL_ID --scope /subscriptions/${AZURE_SUBSCRIPTION_ID}/resourceGroups/${AZURE_RESOURCE_GROUP}
```

4. make quickstart (basically kubectl apply -f config/quickstart/quickstart.yaml

```sh
make quickstart
```
