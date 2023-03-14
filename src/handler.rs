mod handler {}

use crate::events::{Event, KubernetesService, LocallyMappedService, ServiceName};
use crate::hosts::Hosts;
use anyhow::Context;
use anyhow::{bail, Result};
use futures::future::ok;
use futures::{StreamExt, TryStreamExt};
use k8s_openapi::api::core::v1::Pod;
use kube::api::ListParams;
use kube::runtime::watcher;
use kube::{Api, Client};
use std::collections::HashMap;
use std::net::SocketAddr;
use std::sync::Arc;
use std::time::Duration;
use tokio::sync::mpsc;
use tokio::sync::Mutex;
use tokio::{
    io::{AsyncRead, AsyncWrite},
    net::TcpListener,
};
use tokio_stream::wrappers::TcpListenerStream;
use tracing::log::warn;
use tracing::{debug, error, info};

pub struct Handler {
    hosts: Hosts,
    known_services: Vec<KubernetesService>,
    service_pod_mapping: Arc<Mutex<HashMap<ServiceName, String>>>,
}

impl Handler {
    pub fn new(hosts: Hosts) -> Handler {
        Handler {
            hosts,
            known_services: vec![],
            service_pod_mapping: Arc::new(Mutex::new(HashMap::new())),
        }
    }

    // pub async fn sync_service_port_mapping(&self) {
    //     let client = Client::try_default().await.unwrap();
    //     let api: Api<Pod> = Api::all(client);

    //     watcher(api, ListParams::default())
    //         .try_for_each(|watch_event| async {
    //             match watch_event {
    //                 watcher::Event::Applied(pod) => {
    //                     while let Err(e) = self.try_sync_service_port(&pod).await {
    //                         warn!(
    //                             "sync service pod failed for {}/{}: {}",
    //                             &pod.metadata.clone().name.unwrap(),
    //                             &pod.metadata.clone().namespace.unwrap(),
    //                             e
    //                         );
    //                     }
    //                 }
    //                 watcher::Event::Deleted(pod) => warn!("pod deleted, cannot handle this yet"),
    //                 watcher::Event::Restarted(pods) => {
    //                     for pod in pods {
    //                         while let Err(e) = self.try_sync_service_port(&pod).await {
    //                             warn!(
    //                                 "sync service pod failed for {}/{}: {}",
    //                                 &pod.metadata.clone().name.unwrap(),
    //                                 &pod.metadata.clone().namespace.unwrap(),
    //                                 e
    //                             );
    //                         }
    //                     }
    //                 }
    //             }

    //             Ok(())
    //         })
    //         .await
    //         .unwrap();
    // }

    async fn try_sync_service_port(&self, pod: &Pod) -> Result<()> {
        let matching_services: Vec<&KubernetesService> = self
            .known_services
            .iter()
            .filter(|svc| svc.match_pod(pod))
            .collect();

        if matching_services.len() == 0 {
            bail!("no services matched");
        }

        for svc in matching_services {
            self.service_pod_mapping
                .lock()
                .await
                .insert(svc.service_name.clone(), pod.clone().metadata.name.unwrap());
        }

        Ok(())
    }

    pub async fn handle(&mut self, e: Event) -> Result<()> {
        match e {
            Event::AppliedService(svc) => {
                debug!("event: apply svc");

                self.known_services = self
                    .known_services
                    .clone()
                    .into_iter()
                    .filter(|ks| ks.service_name != svc.service_name)
                    .collect();
                self.known_services.push(svc.clone());

                let ip = self.hosts.get_or_create_ip(&svc.service_name.to_string())?;
                info!(
                    "updated service {} -> {}",
                    &svc.service_name.to_string(),
                    ip
                );

                let known_services = self.hosts.get_known_services();

                let local_svc = known_services
                    .iter()
                    .find(|s| s.service_name == svc.service_name);

                if let Some(local_svc) = local_svc {
                    let service_port_mapping = self.service_pod_mapping.clone();
                    tokio::spawn(fwd(local_svc.clone(), svc.clone(), service_port_mapping));
                }
            }
            Event::DeletedService(svc) => {
                debug!("event: delete svc");
                self.hosts.delete(&svc.service_name.to_string())?;
                info!("deleted service {}", &svc.service_name.to_string());
            }
            Event::ResetServices(svcs) => {
                debug!("event: reset svcs");

                self.hosts.reset();

                if let Err(_) = svcs.into_iter().try_for_each(|svc| {
                    self.known_services = self
                        .known_services
                        .clone()
                        .into_iter()
                        .filter(|ks| ks.service_name != svc.service_name)
                        .collect();
                    self.known_services.push(svc.clone());

                    match self.hosts.get_or_create_ip(&svc.service_name.to_string()) {
                        Ok(ip) => {
                            info!(
                                "recreated service {} -> {}",
                                &svc.service_name.to_string(),
                                ip
                            );

                            let known_services = self.hosts.get_known_services();

                            let local_svc = known_services
                                .iter()
                                .find(|s| s.service_name == svc.service_name);

                            if let Some(local_svc) = local_svc {
                                let service_port_mapping = self.service_pod_mapping.clone();
                                tokio::spawn(fwd(
                                    local_svc.clone(),
                                    svc.clone(),
                                    service_port_mapping,
                                ));
                            }

                            return Ok(());
                        }
                        Err(_) => Err(()),
                    }
                }) {
                    bail!("oops")
                }
            }
            Event::AppliedPod(pod) => {
                if let Err(e) = self.try_sync_service_port(&pod).await {
                    warn!(
                        "sync service pod failed for {}/{}: {}",
                        &pod.metadata.clone().name.unwrap(),
                        &pod.metadata.clone().namespace.unwrap(),
                        e
                    );
                }
            }
            Event::DeletedPod(pod) => warn!("pod deleted, cannot handle this yet"),
            Event::ResetPods(pods) => {
                debug!("event: reset pods");
                for pod in pods {
                    if let Err(e) = self.try_sync_service_port(&pod).await {
                        warn!(
                            "sync service pod failed for {}/{}: {}",
                            &pod.metadata.clone().name.unwrap(),
                            &pod.metadata.clone().namespace.unwrap(),
                            e
                        );
                    }
                }
            }
        }

        self.hosts.save()?;

        Ok(())
    }
}

async fn fwd(
    local_svc: LocallyMappedService,
    kubernetes_svc: KubernetesService,
    service_pod_mapping: Arc<Mutex<HashMap<ServiceName, String>>>,
) -> Result<()> {
    let client = Client::try_default().await?;
    let pods = Api::namespaced(client, &local_svc.service_name.ns);

    let pod_name = loop {
        let pod_name = service_pod_mapping.lock().await;

        let pod_name = pod_name
            .get(&local_svc.service_name)
            .clone()
            .context("pod not mapped for service");

        match pod_name {
            Ok(pod_name) => break pod_name.clone(),
            Err(_) => {
                warn!(
                    "could not find pod name for {}",
                    &local_svc.service_name.to_string()
                );
                tokio::time::sleep(Duration::from_millis(500)).await;
                continue;
            }
        };
    };

    for (service_port, pod_port) in kubernetes_svc.service_ports.clone() {
        let stream = TcpListenerStream::new(
            TcpListener::bind(SocketAddr::new(local_svc.ip, service_port as u16)).await?,
        );

        let pods = pods.clone();
        let pod_name = pod_name.clone();

        tokio::spawn(async move {
            match stream
                .try_for_each(|client_conn| async {
                    if let Ok(peer_addr) = client_conn.peer_addr() {
                        info!(%peer_addr, "new connection");
                    }
                    let pods = pods.clone();
                    let pod_name = pod_name.clone();
                    tokio::spawn(async move {
                        if let Err(e) =
                            forward_connection(&pods, &pod_name, pod_port as u16, client_conn).await
                        {
                            error!(
                                error = e.as_ref() as &dyn std::error::Error,
                                "failed to forward connection"
                            );
                        }
                    });
                    // keep the server running
                    Ok(())
                })
                .await
            {
                Ok(_) => info!("listener stream exited"),
                Err(e) => error!("listener stream failed: {}", e),
            };
        });
    }

    Ok(())
}

async fn forward_connection(
    pods: &Api<Pod>,
    pod_name: &str,
    port: u16,
    mut client_conn: impl AsyncRead + AsyncWrite + Unpin,
) -> anyhow::Result<()> {
    let mut forwarder = pods.portforward(pod_name, &[port]).await?;
    let mut upstream_conn = forwarder
        .take_stream(port)
        .context("port not found in forwarder")?;
    tokio::io::copy_bidirectional(&mut client_conn, &mut upstream_conn).await?;
    drop(upstream_conn);
    forwarder.join().await?;
    info!("connection closed");
    Ok(())
}
