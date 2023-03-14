mod hosts {}

use anyhow::{bail, Result};
use std::{
    fs,
    net::{IpAddr, Ipv4Addr},
    str::FromStr,
};
use tracing::{info, warn};

use crate::events::{LocallyMappedService, ServiceName};

// TODO: improve address handling code to allow flexible range config
const MIN_ADDR: Ipv4Addr = Ipv4Addr::new(127, 8, 0, 1);
const MAX_ADDR: Ipv4Addr = Ipv4Addr::new(127, 8, 255, 255);

#[derive(Debug)]
pub struct Hosts {
    entries: Vec<HostsLine>,
    prev_entries: Vec<HostsLine>,
}

#[derive(Debug, Clone)]
enum HostsLine {
    Entry(HostsEntry),
    RawLine(String),
    ManagedStart,
    ManagedEnd,
}

#[derive(Debug, Clone)]
struct HostsEntry {
    pub ip: IpAddr,
    pub names: Vec<String>,
    raw: String,
    managed: bool,
}

impl Hosts {
    pub fn new() -> Result<Hosts> {
        let mut h = Hosts {
            entries: vec![],
            prev_entries: vec![],
        };
        h.reload()?;
        Ok(h)
    }

    pub fn from_str(s: &str) -> Hosts {
        let mut entries: Vec<HostsLine> = vec![];
        let mut managed = false;

        for line in s.lines() {
            let mut e = parse_line(line);
            match e {
                HostsLine::ManagedStart => managed = true,
                HostsLine::ManagedEnd => managed = false,
                HostsLine::Entry(orig) => {
                    e = HostsLine::Entry(HostsEntry {
                        managed: managed,
                        ..orig
                    })
                }
                _ => (),
            }

            entries.push(e)
        }

        Hosts {
            entries,
            prev_entries: vec![],
        }
    }

    pub fn reload(&mut self) -> Result<()> {
        let contents = fs::read_to_string(hosts_path_for_platform())?;
        let new = Hosts::from_str(contents.as_str());

        self.entries = new.entries; // TODO: don't

        Ok(())
    }

    pub fn save(&self) -> Result<()> {
        fs::write(hosts_path_for_platform(), self.encode())?;
        Ok(())
    }

    pub fn encode(&self) -> String {
        self.entries
            .iter()
            .map(|e| e.encode() + "\n")
            .reduce(|acc, l| acc + &l)
            .unwrap_or("".to_string())
    }

    pub fn get_known_services(&self) -> Vec<LocallyMappedService> {
        self.entries
            .iter()
            .filter_map(|e| match e {
                HostsLine::Entry(e) => {
                    if e.managed {
                        Some(e.names.iter().filter_map(|name| {
                            let service_name = match ServiceName::from_string(name) {
                                Ok(s) => Some(s),
                                Err(e) => {
                                    warn!("error mapping managed service name from hosts {}", e);
                                    None
                                }
                            }?;

                            Some(LocallyMappedService {
                                service_name: service_name,
                                ip: e.ip,
                            })
                        }))
                    } else {
                        None
                    }
                }
                _ => None,
            })
            .flatten()
            .collect()
    }

    pub fn get_or_create_ip(&mut self, name: &String) -> Result<IpAddr> {
        match self.get_by_name(&name)? {
            Some((_, e)) => Ok(e.ip),
            None => {
                let ip = match self.previous().get_by_name(&name)? {
                    Some((_, e)) => Ok(e.ip),
                    None => self.available_ip(),
                }?;
                self.set(&name, ip)?;
                Ok(ip)
            }
        }
    }

    pub fn delete(&mut self, name: &String) -> Result<()> {
        let entry = self.get_by_name(&name)?;

        match entry {
            Some((idx, _)) => {
                self.entries.remove(idx);
                Ok(())
            }
            None => Ok(()),
        }
    }

    pub fn reset(&mut self) {
        let entries = self
            .entries
            .iter()
            .filter(|e| match e {
                HostsLine::Entry(e) => !e.managed,
                _ => true,
            })
            .map(|e| e.clone())
            .collect();

        self.prev_entries = self.entries.clone();
        self.entries = entries;
    }

    fn previous(&self) -> Hosts {
        Hosts {
            entries: self.prev_entries.clone(),
            prev_entries: vec![],
        }
    }

    fn get_by_name(&self, name: &String) -> Result<Option<(usize, &HostsEntry)>> {
        let entries_for_name: Vec<(usize, &HostsLine)> = self
            .entries
            .iter()
            .enumerate()
            .filter(|&(_, e)| {
                if let HostsLine::Entry(e) = e {
                    return e.names.contains(&name);
                }
                false
            })
            .collect();

        match entries_for_name.len() {
            0 => Ok(None),
            1 => {
                if let (idx, HostsLine::Entry(e)) = &entries_for_name[0] {
                    if !e.managed {
                        warn!("multiple entries for one name. remove one. {entries_for_name:?}");
                        anyhow::bail!("{e:?} is not a managed entry but we need to use it");
                    };

                    return Ok(Some((*idx, e)));
                } else {
                    panic!("matched an entry that is not a HostsEntry")
                }
            }
            _ => anyhow::bail!("multiple entries for one name. remove one. {entries_for_name:?}"),
        }
    }

    fn get_by_ip(&self, ip: IpAddr) -> Option<&HostsEntry> {
        let found = self.entries.iter().find_map(|e| match e {
            HostsLine::Entry(e) => {
                if e.ip == ip {
                    Some(e)
                } else {
                    None
                }
            }
            _ => None,
        });

        match found {
            Some(e) => Some(e),
            _ => None,
        }
    }

    fn available_ip(&self) -> Result<IpAddr> {
        // TODO IPv6?
        let mut ips: Vec<Ipv4Addr> = self
            .entries
            .iter()
            .filter_map(|e| {
                if let HostsLine::Entry(e) = e {
                    if let IpAddr::V4(ip) = e.ip {
                        if ip.is_loopback() && ip >= MIN_ADDR && ip <= MAX_ADDR {
                            Some(ip)
                        } else {
                            None
                        }
                    } else {
                        None
                    }
                } else {
                    None
                }
            })
            .collect();

        ips.sort();
        let ips = &ips; // no longer mut

        let mut prev: Option<&Ipv4Addr> = None;
        for ip in ips {
            if let Some(prev_ip) = prev {
                if ip.octets()[2..4].iter().sum::<u8>() - prev_ip.octets()[2..4].iter().sum::<u8>()
                    > 1
                {
                    let new_ip = next_ip(*prev_ip)?;

                    debug_assert!(prev_ip < &new_ip);
                    debug_assert!(ip > &new_ip);
                    debug_assert!(ips.contains(&ip));

                    return Ok(new_ip);
                }
            }
            prev = Some(ip);
        }

        match prev {
            Some(ip) => next_ip(*ip),
            None => Ok(IpAddr::V4(MIN_ADDR)),
        }
    }

    fn ensure_managed_idx(&mut self) -> usize {
        let idx = self.entries.iter().enumerate().find(|(idx, e)| {
            if let HostsLine::ManagedStart = e {
                true
            } else {
                false
            }
        });

        match idx {
            Some((idx, _)) => idx,
            None => {
                let idx = self.entries.len();
                self.entries.push(HostsLine::ManagedStart);
                self.entries.push(HostsLine::ManagedEnd);
                idx
            }
        }
    }

    fn set(&mut self, name: &String, ip: IpAddr) -> Result<()> {
        let managed_idx = self.ensure_managed_idx() + 1;
        let entry = self.get_by_name(&name)?;

        // What is going on here: entry has an immutable borrow from self.entries
        // So modifying (mutable borrowing) of self.entries from this match block is not possible.
        // Instead, we store what we want to set in this temporary variable and set it after entry is dropped.
        let mut to_set: Option<(usize, HostsLine)> = None;

        match entry {
            Some((idx, e)) => {
                to_set = Some((
                    idx,
                    HostsLine::Entry(HostsEntry {
                        ip,
                        names: vec![name.to_string()],
                        raw: "".to_string(),
                        managed: true,
                    }),
                ))
            }
            None => self.entries.insert(
                managed_idx,
                HostsLine::Entry(HostsEntry {
                    ip: ip,
                    names: vec![name.to_string()],
                    raw: "".to_string(),
                    managed: true,
                }),
            ),
        }

        if let Some((idx, e)) = to_set {
            self.entries[idx] = e;
        }

        Ok(())
    }
}

// TODO: platforms other than Unix-like ones
fn hosts_path_for_platform() -> &'static str {
    "/etc/hosts"
}

fn next_ip(ip: Ipv4Addr) -> Result<IpAddr> {
    let mut octets = ip.octets().clone();

    if octets[3] == u8::MAX {
        if octets[2] != u8::MAX {
            octets[2] += 1;
        } else {
            bail!("out of IP addresses!")
        }
    } else {
        octets[3] += 1
    }

    Ok(IpAddr::V4(Ipv4Addr::new(
        octets[0], octets[1], octets[2], octets[3],
    )))
}

fn parse_line(line: &str) -> HostsLine {
    let raw = HostsLine::RawLine(line.to_string());

    if line.starts_with("#") {
        match line {
            "# START KUBETRANSPORT MANAGED" => return HostsLine::ManagedStart,
            "# END KUBETRANSPORT MANAGED" => return HostsLine::ManagedEnd,
            _ => return raw,
        }
    }

    let fields = &mut line.split_whitespace();

    let ip: Option<IpAddr>;
    match fields.next() {
        Some(field) => {
            ip = Some(IpAddr::from_str(field).unwrap_or_else(|_| panic!("{}", field)));
        }
        None => return raw,
    };
    let ip = ip.unwrap();

    let first_name: Option<String>;
    match fields.next() {
        Some(field) => {
            first_name = Some(field.to_string());
        }
        None => return raw,
    };
    let first_name = first_name.unwrap();

    let mut names: Vec<String> = fields.map(String::from).collect();
    names.insert(0, first_name);

    HostsLine::Entry(HostsEntry {
        ip,
        names,
        raw: line.to_string(),
        managed: false,
    })
}

impl HostsLine {
    fn encode(&self) -> String {
        match self {
            HostsLine::Entry(entry) => {
                if entry.managed {
                    format!("{} {}", &entry.ip, &entry.names.join(" "))
                } else {
                    entry.raw.to_string()
                }
            }
            HostsLine::RawLine(line) => line.to_string(),
            HostsLine::ManagedStart => "# START KUBETRANSPORT MANAGED".to_string(),
            HostsLine::ManagedEnd => "# END KUBETRANSPORT MANAGED".to_string(),
        }
    }
}

#[cfg(test)]
mod tests {
    use std::net::Ipv4Addr;

    use super::*;

    const BASE_FILE: &str =
        "127.0.0.1   localhost localhost.localdomain localhost4 localhost4.localdomain4
::1         localhost localhost.localdomain localhost6 localhost6.localdomain6

#
# a comment
192.168.122.250 something.local

# START KUBETRANSPORT MANAGED
127.8.0.1 crab.default.svc.cluster.local
127.8.0.2 gopher.default.svc.cluster.local
127.8.0.3 snake.default.svc.cluster.local
# END KUBETRANSPORT MANAGED

127.8.0.5 somethingelse.local
";

    #[test]
    fn roundtrip() {
        let hosts = Hosts::from_str(BASE_FILE);

        assert_eq!(hosts.encode(), BASE_FILE)
    }

    #[test]
    fn clean_set_write() {
        let mut hosts = Hosts::from_str("");
        let name = "elephant.default.svc.cluster.local";

        hosts.get_or_create_ip(&name.to_string()).unwrap();

        assert_eq!(
            hosts.encode(),
            "# START KUBETRANSPORT MANAGED
127.8.0.1 elephant.default.svc.cluster.local
# END KUBETRANSPORT MANAGED
"
        )
    }

    #[test]
    fn set() {
        let mut hosts = Hosts::from_str(BASE_FILE);

        let name = "crab.default.svc.cluster.local";
        let names = vec![name.to_string()];
        let ip = IpAddr::V4(Ipv4Addr::new(127, 8, 0, 10));

        hosts.set(&name.to_string(), ip).unwrap();

        let found: Vec<&HostsLine> = hosts
            .entries
            .iter()
            .filter(|&e| {
                if let HostsLine::Entry(e) = e {
                    return e.names.len() == 1 && e.names[0] == name;
                }
                false
            })
            .collect();

        assert!(found.len() == 1);

        let expected = HostsEntry {
            ip,
            names,
            raw: "".to_string(),
            managed: true,
        };

        if let HostsLine::Entry(found) = found[0] {
            assert_eq!(expected.ip, found.ip);
            assert_eq!(expected.names, found.names);
            assert_eq!(expected.raw, found.raw);
            assert_eq!(expected.managed, found.managed);
        } else {
            panic!("not an entry")
        }
    }

    #[test]
    fn delete() {
        let mut hosts = Hosts::from_str(BASE_FILE);
        let name = "crab.default.svc.cluster.local";
        let length = hosts.entries.len();

        hosts.delete(&name.to_string()).unwrap();

        assert_eq!(length, hosts.entries.len() + 1)
    }

    #[test]
    fn get_ip() {
        let mut hosts = Hosts::from_str(BASE_FILE);
        let name = "snake.default.svc.cluster.local";
        let ip = IpAddr::V4(Ipv4Addr::new(127, 8, 0, 3));

        assert_eq!(ip, hosts.get_or_create_ip(&name.to_string()).unwrap());
    }

    #[test]
    fn create_ip() {
        let mut hosts = Hosts::from_str(BASE_FILE);
        let name = "elephant.default.svc.cluster.local";
        let ip = IpAddr::V4(Ipv4Addr::new(127, 8, 0, 4));

        assert_eq!(ip, hosts.get_or_create_ip(&name.to_string()).unwrap());

        let name = "camel.default.svc.cluster.local";
        let ip = IpAddr::V4(Ipv4Addr::new(127, 8, 0, 6));

        assert_eq!(ip, hosts.get_or_create_ip(&name.to_string()).unwrap());
    }
}
