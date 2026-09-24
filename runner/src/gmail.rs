use crate::{Api, Result};
use base64::{engine::general_purpose::URL_SAFE_NO_PAD, Engine};
use chrono::Utc;
use reqwest::Url;
use serde_json::{json, Map, Value};
use std::collections::HashSet;

const DEFAULT_API: &str = "https://gmail.googleapis.com";

pub(crate) async fn poll_email(
    api: &Api,
    wid: &str,
    version: i64,
    node: &Value,
    checkpoint: &Value,
) -> Result<()> {
    let credential_id = node["credential_id"]
        .as_str()
        .ok_or("email trigger credential missing")?;
    let poll_seconds = node["config"]["poll_seconds"]
        .as_i64()
        .unwrap_or(60)
        .max(30);
    let poll_start = Utc::now().timestamp();
    let last_poll = checkpoint["last_poll_at"].as_i64().unwrap_or(0);
    if last_poll > 0 && poll_start - last_poll < poll_seconds {
        return Ok(());
    }
    let response = api
        .get(
            &format!("/internal/triggers/{wid}/credentials/{credential_id}?version={version}"),
            None,
        )
        .await?;
    let credential = &response["credential"]["data"];
    let token = crate::gmail_token(&api.client, credential).await?;
    let base = std::env::var("GMAIL_API_BASE").unwrap_or_else(|_| DEFAULT_API.into());
    let base = base.trim_end_matches('/');
    let Some(start_history) = checkpoint["history_id"].as_str() else {
        let profile = gmail_get(api, &token, &format!("{base}/gmail/v1/users/me/profile")).await?;
        let history_id = profile["historyId"]
            .as_str()
            .ok_or("Gmail profile missing historyId")?;
        api.post(&format!("/internal/triggers/{wid}/checkpoint"), json!({"version":version,"checkpoint":{"history_id":history_id,"last_poll_at":poll_start}})).await?;
        return Ok(());
    };
    let mut page = String::new();
    let mut ids = Vec::new();
    let mut newest_history = start_history.to_owned();
    loop {
        let mut url =
            Url::parse(&format!("{base}/gmail/v1/users/me/history")).map_err(|e| e.to_string())?;
        url.query_pairs_mut()
            .append_pair("startHistoryId", start_history)
            .append_pair("historyTypes", "messageAdded")
            .append_pair("maxResults", "100");
        if !page.is_empty() {
            url.query_pairs_mut().append_pair("pageToken", &page);
        }
        let response = api
            .client
            .get(url)
            .bearer_auth(&token)
            .send()
            .await
            .map_err(|e| e.to_string())?;
        if response.status().as_u16() == 404 {
            return Err(
                "Gmail history cursor expired; reconnect or reset this trigger explicitly".into(),
            );
        }
        if !response.status().is_success() {
            return Err(format!("Gmail history HTTP {}", response.status()));
        }
        crate::health_touch("triggers");
        let value: Value = response.json().await.map_err(|e| e.to_string())?;
        if let Some(history) = value["history"].as_array() {
            for record in history {
                if let Some(added) = record["messagesAdded"].as_array() {
                    for item in added {
                        if let Some(id) = item["message"]["id"].as_str() {
                            ids.push(id.to_owned());
                        }
                    }
                }
            }
        }
        if let Some(id) = value["historyId"].as_str() {
            newest_history = id.to_owned();
        }
        page = value["nextPageToken"].as_str().unwrap_or("").to_owned();
        if page.is_empty() {
            break;
        }
        if ids.len() > 10_000 {
            return Err("Gmail history backlog exceeds 10,000 messages".into());
        }
    }
    let query = node["config"]["query"].as_str().unwrap_or("in:inbox");
    let matching = if ids.is_empty() || matches!(query, "in:inbox" | "is:unread") {
        None
    } else {
        Some(matching_ids(api, &token, base, query).await?)
    };
    let mut seen = HashSet::new();
    for message_id in ids {
        if !seen.insert(message_id.clone())
            || matching
                .as_ref()
                .is_some_and(|ids| !ids.contains(&message_id))
        {
            continue;
        }
        let message = match gmail_get(
            api,
            &token,
            &format!("{base}/gmail/v1/users/me/messages/{message_id}?format=full"),
        )
        .await
        {
            Ok(message) => message,
            Err(error) if error == "Gmail API HTTP 404" => continue,
            Err(error) => return Err(error),
        };
        let required_label = match query {
            "in:inbox" => Some("INBOX"),
            "is:unread" => Some("UNREAD"),
            _ => None,
        };
        if required_label.is_some_and(|label| {
            !message["labelIds"]
                .as_array()
                .is_some_and(|list| list.iter().any(|value| value == label))
        }) {
            continue;
        }
        let input = normalized_message(&message);
        api.post(
            &format!("/internal/triggers/{wid}/events"),
            json!({"version":version,"event_id":format!("gmail:{message_id}"),"input":input}),
        )
        .await?;
        crate::health_touch("triggers");
    }
    api.post(&format!("/internal/triggers/{wid}/checkpoint"), json!({"version":version,"checkpoint":{"history_id":newest_history,"last_poll_at":poll_start}})).await?;
    Ok(())
}

async fn gmail_get(api: &Api, token: &str, url: &str) -> Result<Value> {
    let response = api
        .client
        .get(url)
        .bearer_auth(token)
        .send()
        .await
        .map_err(|e| e.to_string())?;
    if !response.status().is_success() {
        return Err(format!("Gmail API HTTP {}", response.status().as_u16()));
    }
    crate::health_touch("triggers");
    response.json().await.map_err(|e| e.to_string())
}

async fn matching_ids(api: &Api, token: &str, base: &str, query: &str) -> Result<HashSet<String>> {
    let mut ids = HashSet::new();
    let mut page = String::new();
    loop {
        let mut url =
            Url::parse(&format!("{base}/gmail/v1/users/me/messages")).map_err(|e| e.to_string())?;
        url.query_pairs_mut()
            .append_pair("maxResults", "100")
            .append_pair("q", query);
        if !page.is_empty() {
            url.query_pairs_mut().append_pair("pageToken", &page);
        }
        let value = gmail_get(api, token, url.as_str()).await?;
        if let Some(messages) = value["messages"].as_array() {
            for message in messages {
                if let Some(id) = message["id"].as_str() {
                    ids.insert(id.to_owned());
                }
            }
        }
        page = value["nextPageToken"].as_str().unwrap_or("").to_owned();
        if page.is_empty() {
            break;
        }
        if ids.len() > 10_000 {
            return Err("Gmail query contains over 10,000 messages; narrow query".into());
        }
    }
    Ok(ids)
}

pub(crate) fn normalized_message(message: &Value) -> Value {
    let mut headers = Map::new();
    if let Some(items) = message["payload"]["headers"].as_array() {
        for header in items {
            if let (Some(name), Some(value)) = (header["name"].as_str(), header["value"].as_str()) {
                headers.insert(name.to_ascii_lowercase(), Value::String(value.to_owned()));
            }
        }
    }
    let sender = headers.get("from").cloned().unwrap_or(Value::Null);
    let subject = headers.get("subject").cloned().unwrap_or(Value::Null);
    let body = plain_body(&message["payload"])
        .unwrap_or_else(|| message["snippet"].as_str().unwrap_or("").to_owned());
    json!({"id":message["id"],"thread_id":message["threadId"],"sender":sender,"subject":subject,"body":body,"snippet":message["snippet"],"headers":headers,"message":message})
}

fn plain_body(part: &Value) -> Option<String> {
    if part["mimeType"] == "text/plain" || part["mimeType"].is_null() {
        if let Some(data) = part["body"]["data"].as_str() {
            if let Ok(bytes) = URL_SAFE_NO_PAD.decode(data) {
                if let Ok(text) = String::from_utf8(bytes) {
                    return Some(text);
                }
            }
        }
    }
    if let Some(parts) = part["parts"].as_array() {
        for child in parts {
            if let Some(body) = plain_body(child) {
                return Some(body);
            }
        }
    }
    None
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn normalizes_gmail_message() {
        let body = URL_SAFE_NO_PAD.encode("Hello Ada");
        let message = json!({"id":"m1","threadId":"t1","snippet":"Hello","payload":{"mimeType":"multipart/alternative","headers":[{"name":"From","value":"a@example.com"},{"name":"Subject","value":"Welcome"}],"parts":[{"mimeType":"text/plain","body":{"data":body}}]}});
        let output = normalized_message(&message);
        assert_eq!(output["sender"], "a@example.com");
        assert_eq!(output["subject"], "Welcome");
        assert_eq!(output["body"], "Hello Ada");
    }
}
