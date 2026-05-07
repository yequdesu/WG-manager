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
pub struct InviteInfo {
    pub id: String,
    pub status: String,
    pub created_at: Option<String>,
    pub expires_at: Option<String>,
    pub issued_by: Option<String>,
    pub display_name_hint: Option<String>,
}

#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct InviteListResponse {
    pub invites: Vec<InviteInfo>,
    pub invite_count: Option<i64>,
}

#[derive(Debug, Clone, Serialize)]
pub struct CreateInviteRequest {
    pub name_hint: String,
    pub ttl_hours: u32,
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

    pub async fn get_invites(&self) -> Result<InviteListResponse, String> {
        let url = format!("{}/api/v1/invites", self.base_url);
        let mut req = self.client.get(&url);
        if !self.api_key.is_empty() {
            req = req.header("Authorization", format!("Bearer {}", self.api_key));
        }
        let resp = req.send().await.map_err(|e| e.to_string())?;
        resp.json::<InviteListResponse>()
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

    pub async fn create_invite(&self, name_hint: &str, ttl_hours: u32) -> Result<serde_json::Value, String> {
        let url = format!("{}/api/v1/invites", self.base_url);
        let body = CreateInviteRequest {
            name_hint: name_hint.to_string(),
            ttl_hours,
        };
        let mut req = self.client.post(&url).json(&body);
        if !self.api_key.is_empty() {
            req = req.header("Authorization", format!("Bearer {}", self.api_key));
        }
        let resp = req.send().await.map_err(|e| e.to_string())?;
        resp.json::<serde_json::Value>()
            .await
            .map_err(|e| e.to_string())
    }

    pub async fn revoke_invite(&self, invite_id: &str) -> Result<bool, String> {
        let url = format!("{}/api/v1/invites/{}", self.base_url, invite_id);
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
