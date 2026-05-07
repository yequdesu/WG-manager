use std::env;
use std::fs;
use std::net::IpAddr;
use std::path::PathBuf;

#[derive(Debug, Clone)]
pub struct Config {
    pub api_url: String,
    pub api_key: String,
    /// Full public URL with scheme, e.g. "https://vpn.example.com" or "http://203.0.113.10".
    /// Derived from SERVER_HOST (preferred) or SERVER_PUBLIC_IP in config.env.
    /// Empty if neither is configured.
    pub public_url: String,
}

impl Config {
    pub fn load() -> Self {
        let path = Self::config_path();
        let mut api_url = String::from("http://127.0.0.1:58880");
        let mut api_key = String::new();
        let mut server_host = String::new();
        let mut server_public_ip = String::new();

        if let Ok(content) = fs::read_to_string(&path) {
            for line in content.lines() {
                let line = line.trim();
                if line.starts_with('#') || line.is_empty() {
                    continue;
                }
                if let Some((key, value)) = line.split_once('=') {
                    let val = value.trim();
                    match key.trim() {
                        "MGMT_LISTEN" => {
                            let addr = val.replace("0.0.0.0", "127.0.0.1");
                            api_url = format!("http://{}", addr);
                        }
                        "MGMT_API_KEY" => {
                            api_key = val.to_string();
                        }
                        "SERVER_HOST" => {
                            server_host = val.to_string();
                        }
                        "SERVER_PUBLIC_IP" => {
                            server_public_ip = val.to_string();
                        }
                        _ => {}
                    }
                }
            }
        }

        let public_url = Self::build_public_url(&server_host, &server_public_ip);

        Self { api_url, api_key, public_url }
    }

    /// Mirror the daemon's PublicURL() logic:
    /// - Prefer SERVER_HOST (domain name) → https://server_host
    /// - Fall back to SERVER_PUBLIC_IP (raw IP) → http://ip
    /// - Empty if neither is set
    fn build_public_url(host: &str, ip: &str) -> String {
        let candidate = if !host.is_empty() { host } else { ip };
        if candidate.is_empty() {
            return String::new();
        }
        let is_ip = candidate.parse::<IpAddr>().is_ok();
        if is_ip {
            format!("http://{}", candidate)
        } else {
            format!("https://{}", candidate)
        }
    }

    fn config_path() -> PathBuf {
        let project_dir = env::current_dir()
            .unwrap_or_else(|_| PathBuf::from("."));

        let local_config = project_dir.join("config.env");
        if local_config.exists() {
            return local_config;
        }

        if let Ok(home) = env::var("HOME") {
            let home_config = PathBuf::from(&home).join("WG-manager").join("config.env");
            if home_config.exists() {
                return home_config;
            }
        }

        local_config
    }
}
