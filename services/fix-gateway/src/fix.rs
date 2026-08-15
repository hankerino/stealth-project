//! Minimal FIX 4.4 codec: field encode/decode, message framing, and a UTC
//! timestamp formatter. FIX messages are SOH-delimited `tag=value` fields;
//! the header is 8=BeginString, 9=BodyLength, then the body starting at
//! 35=MsgType, terminated by 10=CheckSum. Pure and unit-tested.

pub const SOH: char = '\u{1}';
pub const BEGIN_STRING: &str = "FIX.4.4";

/// Encode a full FIX message from body fields (which must start at tag 35 and
/// must NOT include 8/9/10). Computes BodyLength (9) and CheckSum (10).
pub fn encode(body_fields: &[(u32, String)]) -> String {
    let mut body = String::new();
    for (tag, val) in body_fields {
        body.push_str(&format!("{tag}={val}{SOH}"));
    }
    let header = format!("8={BEGIN_STRING}{SOH}9={}{SOH}", body.len());
    let without_checksum = format!("{header}{body}");
    let sum: u32 = without_checksum.bytes().map(|b| b as u32).sum();
    format!("{without_checksum}10={:03}{SOH}", sum % 256)
}

/// A parsed FIX message: ordered (tag, value) fields.
#[derive(Debug, Clone, Default)]
pub struct FixMsg {
    pub fields: Vec<(u32, String)>,
}

impl FixMsg {
    pub fn parse(raw: &str) -> FixMsg {
        let mut fields = Vec::new();
        for part in raw.split(SOH) {
            if part.is_empty() {
                continue;
            }
            if let Some((t, v)) = part.split_once('=') {
                if let Ok(tag) = t.parse::<u32>() {
                    fields.push((tag, v.to_string()));
                }
            }
        }
        FixMsg { fields }
    }

    pub fn get(&self, tag: u32) -> Option<&str> {
        self.fields.iter().find(|(t, _)| *t == tag).map(|(_, v)| v.as_str())
    }

    pub fn msg_type(&self) -> Option<&str> {
        self.get(35)
    }
}

/// Extract the first complete FIX message from a byte buffer, returning the
/// message string and the number of bytes consumed. A message ends with the
/// checksum field `10=NNN<SOH>` (7 bytes). Returns None if incomplete.
pub fn frame(buf: &[u8]) -> Option<(String, usize)> {
    let needle = format!("{SOH}10=");
    let nb = needle.as_bytes();
    // find the checksum field start (after a SOH).
    let mut i = 0;
    while i + nb.len() <= buf.len() {
        if &buf[i..i + nb.len()] == nb {
            let end = i + 1 + 7; // SOH + "10=" + 3 digits + SOH
            if end <= buf.len() {
                let msg = String::from_utf8_lossy(&buf[..end]).to_string();
                return Some((msg, end));
            }
            return None;
        }
        i += 1;
    }
    None
}

/// Format a unix-seconds instant as a FIX UTCTimestamp: YYYYMMDD-HH:MM:SS.
pub fn fix_utc(secs: u64) -> String {
    let days = (secs / 86400) as i64;
    let rem = secs % 86400;
    let (h, m, s) = (rem / 3600, (rem % 3600) / 60, rem % 60);
    let (y, mo, d) = civil_from_days(days);
    format!("{y:04}{mo:02}{d:02}-{h:02}:{m:02}:{s:02}")
}

/// Inverse of Howard Hinnant's days_from_civil: unix-days -> (year, month, day).
pub fn civil_from_days(z: i64) -> (i64, i64, i64) {
    let z = z + 719468;
    let era = (if z >= 0 { z } else { z - 146096 }) / 146097;
    let doe = z - era * 146097;
    let yoe = (doe - doe / 1460 + doe / 36524 - doe / 146096) / 365;
    let y = yoe + era * 400;
    let doy = doe - (365 * yoe + yoe / 4 - yoe / 100);
    let mp = (5 * doy + 2) / 153;
    let d = doy - (153 * mp + 2) / 5 + 1;
    let m = if mp < 10 { mp + 3 } else { mp - 9 };
    (if m <= 2 { y + 1 } else { y }, m, d)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn encode_has_header_and_checksum() {
        let raw = encode(&[(35, "A".into()), (49, "CTE".into()), (56, "CLIENT".into())]);
        assert!(raw.starts_with("8=FIX.4.4\u{1}9="));
        assert!(raw.contains("\u{1}35=A\u{1}"));
        assert!(raw.ends_with(|c: char| c == SOH));
        assert!(raw.contains("\u{1}10="));
    }

    #[test]
    fn body_length_is_body_bytes() {
        let raw = encode(&[(35, "0".into())]);
        // body is "35=0\x01" => 5 bytes
        assert!(raw.contains("9=5\u{1}"), "raw={raw}");
    }

    #[test]
    fn checksum_matches_recompute() {
        let raw = encode(&[(35, "D".into()), (11, "ord-1".into()), (55, "H100:us-east-1".into())]);
        let idx = raw.rfind("10=").unwrap();
        let without = &raw[..idx];
        let sum: u32 = without.bytes().map(|b| b as u32).sum::<u32>() % 256;
        assert_eq!(format!("10={:03}\u{1}", sum), &raw[idx..]);
    }

    #[test]
    fn parse_roundtrip() {
        let raw = encode(&[(35, "A".into()), (49, "CTE".into()), (108, "30".into())]);
        let msg = FixMsg::parse(&raw);
        assert_eq!(msg.msg_type(), Some("A"));
        assert_eq!(msg.get(49), Some("CTE"));
        assert_eq!(msg.get(108), Some("30"));
        assert_eq!(msg.get(8), Some("FIX.4.4"));
    }

    #[test]
    fn frame_extracts_one_message() {
        let a = encode(&[(35, "0".into())]);
        let b = encode(&[(35, "1".into()), (112, "t".into())]);
        let mut buf = a.clone().into_bytes();
        buf.extend_from_slice(b.as_bytes());
        let (msg, consumed) = frame(&buf).unwrap();
        assert_eq!(msg, a);
        assert_eq!(consumed, a.len());
        // remainder frames the second message
        let (msg2, _) = frame(&buf[consumed..]).unwrap();
        assert_eq!(msg2, b);
    }

    #[test]
    fn frame_incomplete_returns_none() {
        assert!(frame(b"8=FIX.4.4\x019=5\x0135=0\x01").is_none());
    }

    #[test]
    fn civil_roundtrip() {
        assert_eq!(civil_from_days(0), (1970, 1, 1));
        assert_eq!(civil_from_days(10957), (2000, 1, 1));
        assert_eq!(fix_utc(0), "19700101-00:00:00");
        assert_eq!(fix_utc(10957 * 86400 + 3661), "20000101-01:01:01");
    }
}
