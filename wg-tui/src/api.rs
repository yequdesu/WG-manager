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
    pub alias: Option<String>,
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
    pub redeemed_at: Option<String>,
    pub redeemed_by: Option<String>,
    pub revoked_at: Option<String>,
    pub deleted_at: Option<String>,
    pub deleted_by: Option<String>,
    pub issued_by: Option<String>,
    pub display_name_hint: Option<String>,
    pub dns_override: Option<String>,
    pub pool_name: Option<String>,
    pub target_role: Option<String>,
    pub device_name: Option<String>,
    pub max_uses: Option<i64>,
    pub used_count: Option<i64>,
    pub labels: Option<std::collections::HashMap<String, String>>,
}

#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct InviteListResponse {
    pub invites: Vec<InviteInfo>,
    pub invite_count: Option<i64>,
}

#[derive(Debug, Clone, Serialize, Default)]
pub struct CreateInviteRequest {
    pub name_hint: String,
    pub ttl_hours: u32,
    pub pool_name: Option<String>,
    pub target_role: Option<String>,
    pub device_name: Option<String>,
    pub max_uses: Option<i64>,
    pub labels: Option<std::collections::HashMap<String, String>>,
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
            ..Default::default()
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_invite_info_serialization() {
        let mut labels = std::collections::HashMap::new();
        labels.insert("env".to_string(), "prod".to_string());

        let invite = InviteInfo {
            id: "inv_abc123".to_string(),
            status: "created".to_string(),
            created_at: Some("2026-01-01T00:00:00Z".to_string()),
            expires_at: Some("2026-01-02T00:00:00Z".to_string()),
            redeemed_at: None,
            redeemed_by: None,
            revoked_at: None,
            deleted_at: None,
            deleted_by: None,
            issued_by: Some("admin".to_string()),
            display_name_hint: Some("test-peer".to_string()),
            dns_override: Some("8.8.8.8".to_string()),
            pool_name: Some("default".to_string()),
            target_role: Some("user".to_string()),
            device_name: Some("laptop-01".to_string()),
            max_uses: Some(3),
            used_count: Some(1),
            labels: Some(labels),
        };

        let json = serde_json::to_string(&invite).expect("serialize failed");
        let parsed: InviteInfo = serde_json::from_str(&json).expect("deserialize failed");

        assert_eq!(parsed.id, "inv_abc123");
        assert_eq!(parsed.status, "created");
        assert_eq!(parsed.created_at, Some("2026-01-01T00:00:00Z".to_string()));
        assert_eq!(parsed.expires_at, Some("2026-01-02T00:00:00Z".to_string()));
        assert_eq!(parsed.issued_by, Some("admin".to_string()));
        assert_eq!(parsed.display_name_hint, Some("test-peer".to_string()));
        assert_eq!(parsed.pool_name, Some("default".to_string()));
        assert_eq!(parsed.max_uses, Some(3));
    }

    #[test]
    fn test_invite_info_missing_optionals() {
        let json = r#"{"id":"inv_min","status":"redeemed"}"#;
        let invite: InviteInfo = serde_json::from_str(json).expect("deserialize with missing optionals failed");

        assert_eq!(invite.id, "inv_min");
        assert_eq!(invite.status, "redeemed");
        assert_eq!(invite.created_at, None);
        assert_eq!(invite.expires_at, None);
        assert_eq!(invite.issued_by, None);
        assert_eq!(invite.display_name_hint, None);
    }

    #[test]
    fn test_peer_info_serialization() {
        let peer = PeerInfo {
            alias: None,
            name: "peer1".to_string(),
            address: "10.0.0.2".to_string(),
            dns: Some("1.1.1.1".to_string()),
            public_key: "abc123".to_string(),
            keepalive: Some(25),
            created_at: Some("2026-01-01T00:00:00Z".to_string()),
            endpoint: Some("1.2.3.4:51820".to_string()),
            latest_handshake: Some("2026-01-01T12:00:00Z".to_string()),
            transfer_rx: Some("1.2 GB".to_string()),
            transfer_tx: Some("300 MB".to_string()),
            online: Some(true),
        };

        let json = serde_json::to_string(&peer).expect("serialize failed");
        let parsed: PeerInfo = serde_json::from_str(&json).expect("deserialize failed");

        assert_eq!(parsed.name, "peer1");
        assert_eq!(parsed.address, "10.0.0.2");
        assert_eq!(parsed.online, Some(true));
    }

    #[test]
    fn test_create_invite_request_serialization() {
        let req = CreateInviteRequest {
            name_hint: "my-peer".to_string(),
            ttl_hours: 24,
            pool_name: Some("default".to_string()),
            target_role: Some("user".to_string()),
            device_name: None,
            max_uses: Some(5),
            labels: None,
        };

        let json = serde_json::to_string(&req).expect("serialize failed");
        assert!(json.contains("my-peer"));
        assert!(json.contains("24"));
        assert!(json.contains("default"));
    }
}
