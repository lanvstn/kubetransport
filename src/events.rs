use std::net::IpAddr;

use anyhow::{ensure, Context, Result};
use k8s_openapi::{
    api::core::v1::{Pod, Service, ServiceSpec},
    apimachinery::pkg::util::intstr::IntOrString,
};

mod events {}

#[derive(Debug)]
pub enum Event {
    AppliedService(KubernetesService),
    DeletedService(KubernetesService),
    ResetServices(Vec<KubernetesService>),

    AppliedPod(Pod),
    DeletedPod(Pod),
    ResetPods(Vec<Pod>),
}

#[derive(Debug, Eq, Hash, PartialEq, Clone)]
pub struct ServiceName {
    pub name: String,
    pub ns: String,
}

#[derive(Debug, Clone)]
pub struct KubernetesService {
    pub service_name: ServiceName,
    pub service_ports: Vec<(i32, i32)>,
    spec: ServiceSpec,
}

#[derive(Debug, Eq, Hash, PartialEq, Clone)]
pub struct LocallyMappedService {
    pub service_name: ServiceName,
    pub ip: IpAddr,
}

impl ServiceName {
    pub fn from_string(s: &str) -> Result<ServiceName> {
        let mut parts = s.split(".");
        let name = parts.next().context("no name found")?.to_string();
        let ns = parts.next().context("no namespace found")?.to_string();
        let suffix = parts.fold("".to_string(), |acc, seg| acc + "." + seg);
        ensure!(
            suffix == ".svc.cluster.local",
            format!(
                "svc.cluster.local suffix incorrect: {}.{}.{}",
                name, ns, suffix
            )
        );

        Ok(ServiceName { name, ns })
    }

    pub fn to_string(&self) -> String {
        format!("{}.{}.svc.cluster.local", self.name, self.ns)
    }
}

impl KubernetesService {
    pub fn from_svc(svc: Service) -> KubernetesService {
        let spec = svc.spec.clone().unwrap();

        let ports = spec
            .ports
            .as_ref()
            .unwrap()
            .iter()
            .filter(|p| p.protocol.as_ref().unwrap_or(&"TCP".to_string()) == "TCP")
            .map(|p| {
                (
                    p.port.clone(),
                    match p.target_port.clone().unwrap_or(IntOrString::Int(p.port)) {
                        IntOrString::Int(p) => p,
                        IntOrString::String(_) => p.port,
                    },
                )
            })
            .collect();

        KubernetesService {
            service_name: ServiceName {
                name: svc.metadata.name.as_ref().unwrap().to_string(),
                ns: svc.metadata.namespace.as_ref().unwrap().to_string(),
            },
            service_ports: ports,
            spec: spec,
        }
    }

    pub fn match_pod(&self, pod: &Pod) -> bool {
        self.spec.selector.iter().all(|selector| {
            selector.iter().all(|svc_label| {
                pod.metadata
                    .labels
                    .as_ref()
                    .unwrap()
                    .iter()
                    .any(|pod_label| svc_label == pod_label)
            })
        })
    }
}
