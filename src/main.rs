use std::any;

use futures::{prelude::*, stream::FuturesUnordered};
use handler::Handler;
use hosts::Hosts;
use k8s_openapi::api::core::v1::{Pod, Service};
use kube::{
    api::{Api, ListParams},
    runtime::watcher,
    Client,
};
use tokio::{select, sync::mpsc};
use tracing::info;
// use tracing::*;
use crate::events::{Event, KubernetesService, ServiceName};

mod events;
mod handler;
mod hosts;

#[tokio::main]
async fn main() -> anyhow::Result<()> {
    tracing_subscriber::fmt::init();
    let client = Client::try_default().await?;
    let api = Api::<Service>::all(client);
    let client2 = Client::try_default().await?;
    let api2 = Api::<Pod>::all(client2);

    let (tx1, mut rx1) = mpsc::unbounded_channel::<Event>();
    let (tx2, mut rx2) = mpsc::unbounded_channel::<Event>();

    let hosts = Hosts::new()?;
    let mut handler = Handler::new(hosts);

    tokio::spawn(async move {
        loop {
            tokio::select! {
                e = rx1.recv() => {
                    match e {
                        Some(e) => {
                            handler.handle(e).await;
                        },
                        None => continue
                    }
                }
                e = rx2.recv() => {
                    match e {
                        Some(e) => {
                            handler.handle(e).await;
                        },
                        None => continue
                    }
                }
            }
        }
        // while let Some(e) = rx.recv().await {
        // handler.handle(e);
        // }
    });

    let pod_watcher_handle = tokio::spawn(async move {
        watcher(api2, ListParams::default())
            .try_for_each(|watch_event| async {
                let e: Event;

                match watch_event {
                    watcher::Event::Applied(pod) => {
                        e = Event::AppliedPod(pod);
                    }
                    watcher::Event::Deleted(pod) => {
                        e = Event::DeletedPod(pod);
                    }
                    watcher::Event::Restarted(pods) => {
                        e = Event::ResetPods(pods);
                    }
                }

                tx1.send(e).unwrap();
                Ok(())
            })
            .await;
    });

    // let pod_watcher = watcher(api2, ListParams::default()).try_for_each(|watch_event| async {
    //     let e: Event;

    //     match watch_event {
    //         watcher::Event::Applied(pod) => {
    //             e = Event::AppliedPod(pod);
    //         }
    //         watcher::Event::Deleted(pod) => {
    //             e = Event::DeletedPod(pod);
    //         }
    //         watcher::Event::Restarted(pods) => {
    //             e = Event::ResetPods(pods);
    //         }
    //     }

    //     tx1.send(e).unwrap();
    //     Ok(())
    // });

    let svc_watcher_handle = tokio::spawn(async move {
        watcher(api, ListParams::default())
            .try_for_each(|watch_event| async {
                let e: Event;

                match watch_event {
                    watcher::Event::Applied(svc) => {
                        e = Event::AppliedService(KubernetesService::from_svc(svc));
                    }
                    watcher::Event::Deleted(svc) => {
                        e = Event::DeletedService(KubernetesService::from_svc(svc));
                    }
                    watcher::Event::Restarted(svcs) => {
                        e = Event::ResetServices(
                            svcs.into_iter()
                                .map(KubernetesService::from_svc)
                                .collect::<Vec<KubernetesService>>(),
                        );
                    }
                }

                tx2.send(e).unwrap();
                Ok(())
            })
            .await;
    });

    // let svc_watcher = watcher(api, ListParams::default()).try_for_each(|watch_event| async {
    //     let e: Event;

    //     match watch_event {
    //         watcher::Event::Applied(svc) => {
    //             e = Event::AppliedService(KubernetesService::from_svc(svc));
    //         }
    //         watcher::Event::Deleted(svc) => {
    //             e = Event::DeletedService(KubernetesService::from_svc(svc));
    //         }
    //         watcher::Event::Restarted(svcs) => {
    //             e = Event::ResetServices(
    //                 svcs.into_iter()
    //                     .map(KubernetesService::from_svc)
    //                     .collect::<Vec<KubernetesService>>(),
    //             );
    //         }
    //     }

    //     tx2.send(e).unwrap();
    //     Ok(())
    // });

    // let t1 = tokio::spawn(svc_watcher);
    // let t2 = tokio::spawn(pod_watcher);

    svc_watcher_handle.await.unwrap();
    pod_watcher_handle.await.unwrap();

    Ok(())
}
