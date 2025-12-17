# Alexandria Privacy Policy

**Last Updated: December 17, 2024**

Stout Solutions ("we," "us," or "our") is committed to protecting your privacy. This Privacy Policy explains how we handle information when you use the Alexandria mobile application ("App") and Alexandria Server software ("Server").

**The short version:** We don't collect your data. Everything stays on your device and your own servers.

---

## 1. Our Privacy Philosophy

Alexandria is designed with privacy as a core principle:

- **No data collection** - We don't collect, store, or transmit your personal information
- **No tracking** - We don't track your reading habits, listening history, or app usage
- **No analytics** - We don't use Google Analytics, Firebase, Crashlytics, or similar services
- **No advertising** - We don't show ads or share data with advertisers
- **Local storage only** - Your data stays on your device and your own servers

---

## 2. Information We Do NOT Collect

We want to be explicit about what we don't do:

| Data Type | Collected? |
|-----------|------------|
| Personal information (name, email, phone) | No |
| Location data | No |
| Device identifiers for tracking | No |
| Reading/listening history | No |
| Usage analytics | No |
| Crash reports | No |
| Advertising identifiers | No |
| Contact lists | No |
| Photos, videos, or files | No |
| Health or fitness data | No |
| Financial information | No* |

*Subscription payments are processed entirely by Apple/Google through their app stores. We never see or store payment details.

---

## 3. Information Stored Locally on Your Device

The Alexandria App stores the following data **locally on your device only**:

### 3.1 Server Credentials
- Server URLs you configure
- Usernames and passwords for your servers
- API keys and authentication tokens

**Security:** Credentials are encrypted using:
- **iOS:** Keychain Services (hardware-backed encryption)
- **Android:** EncryptedSharedPreferences (AES-256 encryption)

### 3.2 App Preferences
- Theme and appearance settings
- Playback preferences (speed, skip intervals)
- Display options

### 3.3 Cached Data
- Book covers and images (for faster loading)
- Temporary playback data
- Downloaded content for offline access

### 3.4 Reading/Listening Progress
- Current position in books and audiobooks
- Bookmarks you create

**Note:** This data is stored locally and may also sync to YOUR media servers (not to us).

---

## 4. Alexandria Server (Self-Hosted)

If you choose to run Alexandria Server:

### 4.1 Data Stored on Your Server
The server you host stores:
- User accounts (email, username, password hash)
- Family group information
- Content requests
- Activity data within your family

### 4.2 Your Responsibility
When you run Alexandria Server, YOU are the data controller. You are responsible for:
- Securing your server
- Protecting user data
- Complying with privacy laws (GDPR, CCPA, etc.)
- Informing your family members about data handling

### 4.3 Our Role
We provide the software. We have:
- No access to your server
- No access to data stored on your server
- No ability to retrieve, modify, or delete your data

---

## 5. Third-Party Services

### 5.1 RevenueCat (Subscription Management)
We use RevenueCat to manage subscriptions. RevenueCat may receive:
- Anonymous transaction identifiers
- Subscription status
- App store transaction data

RevenueCat does NOT receive your name, email, or personal information from us. See [RevenueCat's Privacy Policy](https://www.revenuecat.com/privacy/).

### 5.2 Apple App Store / Google Play Store
When you purchase or subscribe, your transaction is processed by Apple or Google. We receive:
- Confirmation that you have a valid subscription
- Anonymous subscriber ID

We do NOT receive your name, email, payment method, or billing address.

### 5.3 Your Media Servers
Alexandria connects to servers you configure (Audiobookshelf, Kavita, Plex, etc.). Your interactions with these servers are governed by their respective privacy policies. We have no visibility into this communication.

---

## 6. Data Transmission

### 6.1 What Data Leaves Your Device
- **To your servers:** Credentials and API requests (to access your content)
- **To RevenueCat:** Anonymous subscription verification
- **To us:** Nothing

### 6.2 Security in Transit
- All connections use HTTPS encryption by default
- HTTP is only permitted for localhost connections
- We enforce certificate validation

---

## 7. Data Retention and Deletion

### 7.1 App Data
All app data is stored locally. To delete it:
1. Log out from all servers in Settings
2. Clear app cache in Settings
3. Uninstall the app

### 7.2 Server Data (Self-Hosted)
If you run Alexandria Server, you control all data. Delete it by:
1. Removing user accounts
2. Deleting the database
3. Stopping and removing the server

---

## 8. Children's Privacy

Alexandria is intended for users aged 13 and older. We do not knowingly collect information from children under 13.

The content accessible through Alexandria depends entirely on what users store on their own servers. Parents/guardians are responsible for the content available to minors in their household.

---

## 9. Your Rights

Depending on your location, you may have rights including:

### 9.1 GDPR Rights (European Users)
- Right to access your data
- Right to rectification
- Right to erasure ("right to be forgotten")
- Right to data portability
- Right to object to processing

### 9.2 CCPA Rights (California Users)
- Right to know what data is collected
- Right to delete your data
- Right to opt-out of data sales
- Right to non-discrimination

### 9.3 Exercising Your Rights
Since we don't collect your data, most of these rights are automatically satisfied. For server data you host yourself, you have full control to access, modify, or delete it.

For subscription-related inquiries, contact your app store (Apple/Google).

---

## 10. International Users

Alexandria is operated from the United States. If you use Alexandria from outside the US:
- Your data remains on your device and your servers
- No data is transferred to us regardless of your location
- You are responsible for compliance with local laws when self-hosting

---

## 11. Security Measures

We implement security best practices:

| Measure | Implementation |
|---------|----------------|
| Credential encryption | AES-256 / Keychain |
| Transport security | HTTPS enforced |
| No cloud backup of credentials | Excluded from device backups |
| Minimal permissions | Only what's needed |
| No tracking SDKs | None included |
| Code obfuscation | ProGuard (Android) |

---

## 12. Changes to This Policy

We may update this Privacy Policy periodically. Changes will be indicated by updating the "Last Updated" date.

Material changes will be communicated through:
- In-app notification
- Updated policy in the app

Continued use after changes constitutes acceptance of the updated policy.

---

## 13. Contact Us

For privacy-related questions or concerns:

**Email:** stoutservers@gmail.com

**Mailing Address:**
Stout Solutions
[Your Address]

---

## 14. Summary

| Question | Answer |
|----------|--------|
| Do you collect my data? | No |
| Do you track me? | No |
| Do you sell my data? | No (we don't have any) |
| Where is my data stored? | On your device and your servers only |
| Can you access my servers? | No |
| Is my data encrypted? | Yes, credentials are encrypted locally |
| How do I delete my data? | Uninstall the app / delete your server |

---

**Your privacy is not just a feature—it's fundamental to how Alexandria is built.**
