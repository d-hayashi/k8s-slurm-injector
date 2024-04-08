apiVersion: admissionregistration.k8s.io/v1
kind: MutatingWebhookConfiguration
metadata:
  name: k8s-slurm-injector-webhook
  labels:
    app: k8s-slurm-injector-webhook
    kind: mutator
webhooks:
  - name: inject-slurm-job.d-hayashi.dev
    objectSelector:
      matchExpressions:
        - key: app
          operator: NotIn
          values: [ "k8s-slurm-injector" ]
    admissionReviewVersions: ["v1"]
    sideEffects: None
    clientConfig:
      service:
        name: k8s-slurm-injector
        namespace: k8s-slurm-injector
        path: /wh/mutating/injectsidecar
      caBundle: CA_BUNDLE
    rules:
      - operations: ["CREATE"]
        apiGroups: ["*"]
        apiVersions: ["*"]
        resources: ["pods"]
