apiVersion: admissionregistration.k8s.io/v1
kind: MutatingWebhookConfiguration
metadata:
  name: k8s-slurm-injector-webhook
  labels:
    app: k8s-slurm-injector-webhook
    kind: mutator
webhooks:
  - name: inject-sidecar-webhook.d-hayashi.dev
    admissionReviewVersions: ["v1"]
    sideEffects: None
    clientConfig:
      service:
        name: k8s-slurm-injector
        namespace: k8s-slurm-injector
        path: /wh/mutating/injectsidecar
      caBundle: CA_BUNDLE
    rules:
      - operations: ["CREATE", "UPDATE"]
        apiGroups: ["*"]
        apiVersions: ["*"]
        resources: ["cronjobs", "jobs", "pods"]