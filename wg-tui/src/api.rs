use serde::{Deserialize, Serialize};

#[derive(Debug, Clone)]
pub struct ApiClient {
    pub base_url: String,
    pub api_key: String,
    client: reqwest::Client,
}

#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct PeerInfo {
    pub name: String,
    pub address: String,
    pub dns: Option<String>,
    pub public_key: String,
    pub keepalive: Option<i64>,
    pub created_at: Option<String>,
    pub endpoint: Option<String>,
    pub latest_handshake: Option<String>,
    pub transfer_rx: Option<String>,
    pub transfer_tx: Option<String>,
    pub online: Option<bool>,
}

#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct PeerListResponse {
    pub peers: Vec<PeerInfo>,
    pub peer_count: Option<i64>,
}

#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct RequestInfo {
    pub id: String,
    pub hostname: String,
    pub address: String,
    pub dns: Option<String>,
    pub source_ip: Option<String>,
    pub created_at: Option<String>,
    pub expires_at: Option<String>,
}

#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct RequestListResponse {
    pub requests: Vec<RequestInfo>,
    pub pending_count: Option<i64>,
}

#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct ServerStatus {
    #[serde(default)]
    pub daemon: String,
    #[serde(default)]
    pub wireguard: String,
    #[serde(default)]
    pub interface: String,
    #[serde(default)]
    pub listen_port: String,
    #[serde(default)]
    pub peer_online: i64,
    #[serde(default)]
    pub peer_total: i64,
}

impl ApiClient {
    pub fn new(base_url: String, api_key: String) -> Self {
        let client = reqwest::Client::builder()
            .timeout(std::time::Duration::from_secs(5))
            .build()
            .expect("failed to create HTTP client");
        Self {
            base_url,
            api_key,
            client,
        }
    }

    pub async fn get_peers(&self) -> Result<PeerListResponse, String> {
        let url = format!("{}/api/v1/peers", self.base_url);
        let mut req = self.client.get(&url);
        if !self.api_key.is_empty() {
            req = req.header("Authorization", format!("Bearer {}", self.api_key));
        }
        let resp = req.send().await.map_err(|e| e.to_string())?;
        resp.json::<PeerListResponse>()
            .await
            .map_err(|e| e.to_string())
    }

    pub async fn get_requests(&self) -> Result<RequestListResponse, String> {
        let url = format!("{}/api/v1/requests", self.base_url);
        let mut req = self.client.get(&url);
        if !self.api_key.is_empty() {
            req = req.header("Authorization", format!("Bearer {}", self.api_key));
        }
        let resp = req.send().await.map_err(|e| e.to_string())?;
        resp.json::<RequestListResponse>()
            .await
            .map_err(|e| e.to_string())
    }

    pub async fn get_status(&self) -> Result<ServerStatus, String> {
        let url = format!("{}/api/v1/status", self.base_url);
        let mut req = self.client.get(&url);
        if !self.api_key.is_empty() {
            req = req.header("Authorization", format!("Bearer {}", self.api_key));
        }
        let resp = req.send().await.map_err(|e| e.to_string())?;
        resp.json::<ServerStatus>()
            .await
            .map_err(|e| e.to_string())
    }

    pub async fn approve_request(&self, request_id: &str) -> Result<bool, String> {
        let url = format!(
            "{}/api/v1/requests/{}/approve",
            self.base_url, request_id
        );
        let mut req = self.client.post(&url);
        if !self.api_key.is_empty() {
            req = req.header("Authorization", format!("Bearer {}", self.api_key));
        }
        let resp = req.send().await.map_err(|e| e.to_string())?;
        let body: serde_json::Value = resp.json().await.map_err(|e| e.to_string())?;
        Ok(body.get("success").and_then(|v| v.as_bool()).unwrap_or(false))
    }

    pub async fn deny_request(&self, request_id: &str) -> Result<bool, String> {
        let url = format!("{}/api/v1/requests/{}", self.base_url, request_id);
        let mut req = self.client.delete(&url);
        if !self.api_key.is_empty() {
            req = req.header("Authorization", format!("Bearer {}", self.api_key));
        }
        let resp = req.send().await.map_err(|e| e.to_string())?;
        let body: serde_json::Value = resp.json().await.map_err(|e| e.to_string())?;
        Ok(body.get("success").and_then(|v| v.as_bool()).unwrap_or(false))
    }

    pub async fn delete_peer(&self, peer_name: &str) -> Result<bool, String> {
        let url = format!("{}/api/v1/peers/{}", self.base_url, peer_name);
        let mut req = self.client.delete(&url);
        if !self.api_key.is_empty() {
            req = req.header("Authorization", format!("Bearer {}", self.api_key));
        }
        let resp = req.send().await.map_err(|e| e.to_string())?;
        let body: serde_json::Value = resp.json().await.map_err(|e| e.to_string())?;
        Ok(body.get("success").and_then(|v| v.as_bool()).unwrap_or(false))
    }
}
