# k8s-slurm-injector

A [Kubernetes admission webhook][k8s-admission-webhooks] that injects a slurm job when a Pod is created.

This is implemented based on [slok/k8s-webhook-example][k8s-webhook-example].


## Deployment

### Prepare ssh-key
You need to generate a new ssh-key as `k8s-slurm-injector` pod must be able to login to a node running slurmd via ssh.

```bash
$ ssh-keygen
(follow the wizard and `id_rsa` and `id_rsa.pub` will be generated)

```

Then, copy the content of `id_rsa.pub` into `${HOME}/.ssh/authorized_keys` on a node where slurmd is running.  

After that, create a secret containing `id_rsa` as follows:

```bash
$ kubectl create ns k8s-slurm-injector
$ kubectl -n k8s-slurm-injector create secret generic k8s-slurm-injector-ssh-id-rsa --from-file=./

```

Make sure that `k8s-slurm-injector-ssh-id-rsa` exists in namespace `k8s-slurm-injector`.

```bash
$ kubectl -n k8s-slurm-injector get secrets
...
k8s-slurm-injector-ssh-id-rsa   Opaque                                2      1m
...

```


### Deploy
Clone this repository and generate certificates for deployment.

```bash
$ git clone https://github.com/d-hayashi/k8s-slurm-injector.git
$ cd k8s-slurm-injector
$ make gen-deploy-certs

```

Then, replace the following parts in `deploy/app.yaml`
- `<SSH Destination>`: Username and IP-address of the node running slurm with format `username@ip-address`
- `<SSH Port>`: Port number of the SSH-server

After that, deploy `app-certs` and `app` and make sure deployment `k8s-slurm-injector` becomes ready.

```bash
$ cd deploy
$ kubectl apply -f app-certs.yaml
$ kubectl apply -f app.yaml
$ kubectl -n k8s-slurm-injector get deployment --watch
NAME                 READY   UP-TO-DATE   AVAILABLE   AGE
k8s-slurm-injector   0/1     1            0           1s
k8s-slurm-injector   1/1     1            1           10s

```

When it is confirmed, deploy `webhooks`.
```bash
$ kubectl apply -f webhooks.yaml

```

If deployment `k8s-slurm-injector` does not become ready, you should check logs.
```bash
$ kubectl -n k8s-slurm-injector logs deployment/k8s-slurm-injector

```


### Test

To check the behavior of k8s-slurm-injector, you can deploy `sample-pod`.
```bash
$ kubectl apply -f sample-pod.yaml

```

Note that pod is labeled with `k8s-slurm-injector/injection: enabled`.  
Slurm jobs are injected only if resources have this label.


## Webhooks

### `inject-slurm-job.d-hayashi.dev`

- Webhook type: Mutating.
- Resources affected: `cronjobs`, `jobs`, `pods`

This webhook injects a slurm-job at the time containers start in the pod.


[k8s-admission-webhooks]: https://kubernetes.io/docs/reference/access-authn-authz/extensible-admission-controllers/
[k8s-webhook-example]: https://github.com/slok/k8s-webhook-example
