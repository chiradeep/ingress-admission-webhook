# Kubernetes Ingress Admission Webhook 

Build and deploy an [AdmissionWebhook](https://kubernetes.io/docs/reference/access-authn-authz/extensible-admission-controllers/#admission-webhooks) to add an annotation to an Ingress object

The Citrix Ingress controller allows you to specify the port for your ingress object with the annotations `ingress.citrix.com/secure-port` and `ingress.citrix.com/insecure-port`. When multiple ingress classes are handled by the same Citrix Ingress controller instance, it is desirable to allocate different ports for different ingress classes.

The MutatingAdmissionWebhook in this code (see `updateAnnotation` in `webhook.go`) uses a harcoded map of class to port (`ingressClassToInsecurePortMap`) to add the required port annotations when the Ingress is being created.

## Prerequisites

Kubernetes 1.14.0 or above with the `admissionregistration.k8s.io/v1beta1` API enabled. Verify that by the following command:
```
kubectl api-versions | grep admissionregistration.k8s.io/v1beta1
```
The result should be:
```
admissionregistration.k8s.io/v1beta1
```

In addition, the `MutatingAdmissionWebhook` and `ValidatingAdmissionWebhook` admission controllers should be added and listed in the correct order in the admission-control flag of kube-apiserver.
With Minikube, this is done by starting minkube with 

```
minikube start --extra-config=apiserver.enable-admission-plugins=NamespaceLifecycle,LimitRanger,ServiceAccount,DefaultStorageClass,DefaultTolerationSeconds,NodeRestriction,MutatingAdmissionWebhook,ValidatingAdmissionWebhook`
```
## Build

1. Setup dep

   The repo uses [dep](https://github.com/golang/dep) as the dependency management tool for its Go codebase. Install `dep` by the following command:
```
go get -u github.com/golang/dep/cmd/dep
```

2. Build and push docker image
   
```
./build
```

3. Create the cert that the Admission webhook will use to be trusted by the API server

```
$ ./deployment/webhook-create-signed-cert.sh
creating certs in tmpdir /var/folders/9t/8ww_c05d2kb6ncr0yxq5t3wm0000gn/T/tmp.bCyMsvXc 
Generating RSA private key, 2048 bit long modulus
..............+++
...........................................+++
e is 65537 (0x10001)
certificatesigningrequest.certificates.k8s.io/admission-webhook-example-svc.default created
NAME                                    AGE   REQUESTOR       CONDITION
admission-webhook-example-svc.default   0s    minikube-user   Pending
certificatesigningrequest.certificates.k8s.io/admission-webhook-example-svc.default approved
secret/admission-webhook-example-certs created
```

4. Deploy the container and service that will serve the mutating webhook. Note: edit `deployment/deployment.yaml` to replace the container `chiradeep/admission-webhook-example:v1` with your container built in step 2.

```
kubectl create -f deployment/deployment.yaml
deployment.apps "admission-webhook-example-deployment" created

$ kubectl create -f deployment/service.yaml
service "admission-webhook-example-svc" created

```
5. Create the admission webhook configuration

```
$ cat ./deployment/mutatingwebhook.yaml | ./deployment/webhook-patch-ca-bundle.sh > ./deployment/mutatingwebhook-ca-bundle.yaml
$ kubectl create -f deployment/mutatingwebhook-ca-bundle.yaml
mutatingwebhookconfiguration.admissionregistration.k8s.io "mutating-webhook-example-cfg" created

```
6. Create a sample ingress with ingress class `class1`. This class is presented in the hardcoded map in `webhook.go`

```
$ kubectl create -f deployment/ingress1.yaml 
ingress.networking.k8s.io/test-ingress created

$ kubectl get ing -o yaml
apiVersion: v1
items:
- apiVersion: extensions/v1beta1
  kind: Ingress
  metadata:
    annotations:
      ingress.citrix.com/insecure-port: "81"
      ingress.citrix.com/secure-port: "4443"
      kubernetes.io/ingress.class: class1
    creationTimestamp: "2019-10-04T06:19:14Z"
    generation: 1
    name: test-ingress
    namespace: default
    resourceVersion: "3684"
    selfLink: /apis/extensions/v1beta1/namespaces/default/ingresses/test-ingress
    uid: a9e354f3-8fc9-4c15-839a-1edcce70c9e1
  spec:
    rules:
    - http:
        paths:
        - backend:
            serviceName: test
            servicePort: 80
          path: /testpath
  status:
    loadBalancer: {}
kind: List
metadata:
  resourceVersion: ""
  selfLink: ""

```

## Credits
Code adapted from https://banzaicloud.com/blog/k8s-admission-webhooks/
