Tutorial

1. Create cluster

2. Authenticate with AAD Pod Identity 
  1. create identity, if not already created
  2. assign roles
  3. deploy necessary stuff
  4. create k8s resources

3. Create config map

4. Deploy controller

| setting | description | default |
| ------- | ----------- | ------- |
| `syncDirection` | Direction of synchronization. Default is `arm-to-node`. Other options are `two-way` and `node-to-arm`. Currently only `arm-to-node` is fully implemented and tested. | `arm-to-node` |
| `labelPrefix` | The node label prefix. An empty prefix will be permitted. However if you use an empty prefix, node labels will not be deleted when the corresponding ARM tag is deleted so using a non-empty prefix is strongly recommended. | `azure.tags` |
| `conflictPolicy` | The policy for conflicting tag/label values. ARM tags or node labels can be given priority. ARM tags have priority by default (`arm-precedence`). Another option is to not update tags and raise Kubernetes event (`ignore`) and `node-precedence`. If set to `node-precedence`, labels will not be deleted when the corresponding tags are deleted, even if `syncDirection` is set to `arm-to-node`. | `arm-precedence` |

