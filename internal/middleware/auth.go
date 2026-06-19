package middleware

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rsa"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
)

// AuthContext holds resolved authentication and workspace parameters.
type AuthContext struct {
	TenantID    string
	UserID      string
	WorkspaceID string
	Role        string
	DBURL       string
}

// ApiKeyRow maps to elements in the api_keys_db.json file.
type ApiKeyRow struct {
	TokenHash      string `json:"tokenHash"`
	EncryptedDbURL string `json:"encryptedDbUrl"`
	TenantID       string `json:"tenantId"`
}

// JWK represents a JSON Web Key in JWKS.
type JWK struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// JWKS represents the JSON Web Key Set.
type JWKS struct {
	Keys []JWK `json:"keys"`
}

type ClerkAuthMiddleware struct {
	db        *sql.DB
	jwksURL   string
	jwksKeys  map[string]*rsa.PublicKey
	jwksMutex sync.RWMutex
	lastFetch time.Time
}

func NewClerkAuthMiddleware(db *sql.DB) *ClerkAuthMiddleware {
	// Dynamically resolve Clerk JWKS endpoint from environment or fallback
	publishableKey := os.Getenv("NEXT_PUBLIC_CLERK_PUBLISHABLE_KEY")
	domain := "smart-mackerel-28.clerk.accounts.dev"
	if publishableKey != "" {
		parts := strings.Split(publishableKey, "_")
		if len(parts) >= 3 {
			decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSuffix(parts[2], "$"))
			if err == nil && len(decoded) > 0 {
				domain = string(decoded)
			}
		}
	}
	jwksURL := fmt.Sprintf("https://%s/.well-known/jwks.json", domain)

	return &ClerkAuthMiddleware{
		db:       db,
		jwksURL:  jwksURL,
		jwksKeys: make(map[string]*rsa.PublicKey),
	}
}

func (m *ClerkAuthMiddleware) fetchJWKS() error {
	// 1. Double-Checked Locking (First Check: Read Lock)
	m.jwksMutex.RLock()
	lastFetch := m.lastFetch
	hasKeys := len(m.jwksKeys) > 0
	m.jwksMutex.RUnlock()

	// Rate limit fetches to once every 5 minutes if we already have keys
	if time.Since(lastFetch) < 5*time.Minute && hasKeys {
		return nil
	}

	// 2. Perform HTTP fetch OUTSIDE the mutex lock to prevent blocking throughput
	client := &http.Client{
		Timeout: 5 * time.Second, // Enforce strict timeout
	}
	resp, err := client.Get(m.jwksURL)
	if err != nil {
		return fmt.Errorf("failed to fetch JWKS from %s: %w", m.jwksURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("JWKS fetch returned status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	var jwks JWKS
	if err := json.Unmarshal(body, &jwks); err != nil {
		return err
	}

	newKeys := make(map[string]*rsa.PublicKey)
	for _, key := range jwks.Keys {
		if key.Kty == "RSA" && key.Kid != "" {
			pubKey, err := getRSAPublicKey(key.N, key.E)
			if err == nil {
				newKeys[key.Kid] = pubKey
			}
		}
	}

	// 3. Acquire Write Lock ONLY for memory update (Second Check)
	m.jwksMutex.Lock()
	// Check if another thread succeeded while we were fetching
	if time.Since(m.lastFetch) < 5*time.Minute && len(m.jwksKeys) > 0 {
		m.jwksMutex.Unlock()
		return nil
	}
	m.jwksKeys = newKeys
	m.lastFetch = time.Now()
	m.jwksMutex.Unlock()

	return nil
}

func (m *ClerkAuthMiddleware) getPublicKey(kid string) (*rsa.PublicKey, error) {
	m.jwksMutex.RLock()
	pubKey, exists := m.jwksKeys[kid]
	m.jwksMutex.RUnlock()

	if exists {
		return pubKey, nil
	}

	// Try fetching new keys if not found
	if err := m.fetchJWKS(); err != nil {
		return nil, err
	}

	m.jwksMutex.RLock()
	pubKey, exists = m.jwksKeys[kid]
	m.jwksMutex.RUnlock()

	if !exists {
		return nil, fmt.Errorf("key id %s not found in JWKS", kid)
	}

	return pubKey, nil
}

func getRSAPublicKey(nStr, eStr string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nStr)
	if err != nil {
		return nil, err
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eStr)
	if err != nil {
		return nil, err
	}
	if len(eBytes) < 4 {
		pad := make([]byte, 4-len(eBytes))
		eBytes = append(pad, eBytes...)
	}

	var eVal uint32
	err = binary.Read(bytes.NewReader(eBytes), binary.BigEndian, &eVal)
	if err != nil {
		return nil, err
	}

	n := new(big.Int).SetBytes(nBytes)
	return &rsa.PublicKey{
		N: n,
		E: int(eVal),
	}, nil
}

func decryptDatabaseURL(encryptedStr string) (string, error) {
	parts := strings.Split(encryptedStr, ":")
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid encrypted dbUrl format")
	}

	ivBytes, err := hex.DecodeString(parts[0])
	if err != nil {
		return "", err
	}
	authTagBytes, err := hex.DecodeString(parts[1])
	if err != nil {
		return "", err
	}
	ciphertextBytes, err := hex.DecodeString(parts[2])
	if err != nil {
		return "", err
	}

	masterKeyHex := os.Getenv("NACL_MASTER_KEY")
	if masterKeyHex == "" {
		masterKeyHex = "7fe7e00b5681f48603a70717d39d8166a512ca211d8b62ac678a8b9abd6260c5"
	}
	keyBytes, err := hex.DecodeString(masterKeyHex)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(keyBytes)
	if err != nil {
		return "", err
	}

	aesgcm, err := cipher.NewGCMWithNonceSize(block, len(ivBytes))
	if err != nil {
		return "", err
	}

	combinedCiphertext := append(ciphertextBytes, authTagBytes...)

	decryptedBytes, err := aesgcm.Open(nil, ivBytes, combinedCiphertext, nil)
	if err != nil {
		return "", err
	}

	return string(decryptedBytes), nil
}

func authenticateM2MToken(token string) (string, string, error) {
	h := sha256.New()
	h.Write([]byte(token))
	tokenHash := hex.EncodeToString(h.Sum(nil))

	dbPath := "/home/pavan/Documents/nacl-dashboard/data/api_keys_db.json"
	data, err := os.ReadFile(dbPath)
	if err != nil {
		return "", "", fmt.Errorf("cannot read api_keys_db.json: %w", err)
	}

	var keys []ApiKeyRow
	if err := json.Unmarshal(data, &keys); err != nil {
		return "", "", fmt.Errorf("corrupt api_keys_db.json: %w", err)
	}

	for _, k := range keys {
		if k.TokenHash == tokenHash {
			dbURL, err := decryptDatabaseURL(k.EncryptedDbURL)
			if err != nil {
				return "", "", fmt.Errorf("failed to decrypt database URL: %w", err)
			}
			return dbURL, k.TenantID, nil
		}
	}

	return "", "", fmt.Errorf("invalid or expired token")
}

func (m *ClerkAuthMiddleware) resolveWorkspaceAndRole(clerkUserID string, requestedWorkspaceID string) (string, string, error) {
	// Query memberships
	rows, err := m.db.Query("SELECT workspace_id, role FROM workspace_members WHERE user_id = $1", clerkUserID)
	if err != nil {
		return "", "", err
	}
	defer rows.Close()

	type membership struct {
		workspaceID string
		role        string
	}

	var memberships []membership
	for rows.Next() {
		var m membership
		if err := rows.Scan(&m.workspaceID, &m.role); err == nil {
			memberships = append(memberships, m)
		}
	}

	// If no memberships, auto-create a personal sandbox
	if len(memberships) == 0 {
		personalWSID := fmt.Sprintf("personal-%s", strings.Replace(clerkUserID, "user_", "", 1))
		personalWSName := "Personal Sandbox"

		tx, err := m.db.Begin()
		if err != nil {
			return "", "", err
		}
		defer tx.Rollback()

		_, err = tx.Exec(
			"INSERT INTO workspaces (id, name, owner_id) VALUES ($1, $2, $3) ON CONFLICT (id) DO NOTHING",
			personalWSID, personalWSName, clerkUserID,
		)
		if err != nil {
			return "", "", err
		}

		_, err = tx.Exec(
			"INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1, $2, $3) ON CONFLICT (workspace_id, user_id) DO NOTHING",
			personalWSID, clerkUserID, "Admin",
		)
		if err != nil {
			return "", "", err
		}

		if err := tx.Commit(); err != nil {
			return "", "", err
		}

		return personalWSID, "Admin", nil
	}

	if requestedWorkspaceID != "" {
		for _, mem := range memberships {
			if mem.workspaceID == requestedWorkspaceID {
				return mem.workspaceID, mem.role, nil
			}
		}
	}

	// Fallback to first membership
	return memberships[0].workspaceID, memberships[0].role, nil
}

func (m *ClerkAuthMiddleware) Authenticate() fiber.Handler {
	return func(c *fiber.Ctx) error {
		var clerkUserID string
		var tenantID string
		var workspaceID string
		var role string
		var dbURL string

		// 1. Check direct headers (development / staging / CLI bypass context)
		if uid := c.Get("x-clerk-user-id"); uid != "" {
			clerkUserID = uid
		}
		if tid := c.Get("x-tenant-id"); tid != "" {
			tenantID = tid
		}

		// 2. Extract Authorization Header
		authHeader := c.Get("Authorization")
		if authHeader != "" && strings.HasPrefix(authHeader, "Bearer ") {
			token := strings.TrimPrefix(authHeader, "Bearer ")

			// Check if it is a JWT (contains three dot-separated segments)
			if strings.Count(token, ".") == 2 {
				// Parse and validate Clerk JWT
				parsedToken, err := jwt.Parse(token, func(t *jwt.Token) (interface{}, error) {
					if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
						return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
					}
					kid, ok := t.Header["kid"].(string)
					if !ok {
						return nil, fmt.Errorf("missing kid in token header")
					}
					return m.getPublicKey(kid)
				})

				if err == nil && parsedToken.Valid {
					if claims, ok := parsedToken.Claims.(jwt.MapClaims); ok {
						if sub, ok := claims["sub"].(string); ok {
							clerkUserID = sub
						}
					}
				} else {
					fmt.Printf("JWT parsing failed: %v\n", err)
				}
			} else {
				// Treat as M2M API Key
				m2mDbURL, m2mTenantID, err := authenticateM2MToken(token)
				if err == nil {
					dbURL = m2mDbURL
					tenantID = m2mTenantID
					clerkUserID = "m2m-agent"
					role = "Admin"
					workspaceID = m2mTenantID
				}
			}
		}

		// If we couldn't resolve a user ID or tenant, return Unauthorized
		if clerkUserID == "" && tenantID == "" {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
				"error": "Missing x-clerk-user-id header",
			})
		}

		clerkUserEmail := c.Get("x-clerk-user-email")
		if clerkUserID != "" && clerkUserEmail != "" {
			_ = m.autoAcceptInvitations(c.UserContext(), clerkUserID, clerkUserEmail)
		}

		// Resolve workspace context if clerkUserID is active and we don't have workspaceID set
		if clerkUserID != "" && workspaceID == "" {
			reqWS := c.Get("x-active-workspace-id")
			resolvedWS, resolvedRole, err := m.resolveWorkspaceAndRole(clerkUserID, reqWS)
			if err == nil {
				workspaceID = resolvedWS
				role = resolvedRole
				tenantID = resolvedWS // Workspace acts as the tenant ID context
			} else if tenantID == "" {
				return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
					"error": fmt.Sprintf("Access denied: No workspace available. Error: %v", err),
				})
			}
		}

		// Set context variables
		c.Locals("clerk_user_id", clerkUserID)
		c.Locals("clerk_user_email", clerkUserEmail)
		c.Locals("tenant_id", tenantID)
		c.Locals("workspace_id", workspaceID)
		c.Locals("role", role)
		c.Locals("db_url", dbURL)

		return c.Next()
	}
}

func (m *ClerkAuthMiddleware) autoAcceptInvitations(ctx context.Context, userID, email string) error {
	rows, err := m.db.QueryContext(ctx, `
		SELECT id, workspace_id, role 
		FROM workspace_invitations 
		WHERE LOWER(email) = LOWER($1) AND status = 'pending' AND expires_at > CURRENT_TIMESTAMP
	`, email)
	if err != nil {
		return err
	}
	defer rows.Close()

	type invite struct {
		id          string
		workspaceID string
		role        string
	}
	var pendingInvites []invite
	for rows.Next() {
		var inv invite
		if err := rows.Scan(&inv.id, &inv.workspaceID, &inv.role); err == nil {
			pendingInvites = append(pendingInvites, inv)
		}
	}

	for _, inv := range pendingInvites {
		tx, err := m.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}

		// 1. Update invitation status to accepted
		_, err = tx.ExecContext(ctx, "UPDATE workspace_invitations SET status = 'accepted' WHERE id = $1", inv.id)
		if err != nil {
			tx.Rollback()
			return err
		}

		// 2. Insert member into workspace_members
		_, err = tx.ExecContext(ctx, `
			INSERT INTO workspace_members (workspace_id, user_id, role)
			VALUES ($1, $2, $3)
			ON CONFLICT (workspace_id, user_id)
			DO UPDATE SET role = EXCLUDED.role
		`, inv.workspaceID, userID, inv.role)
		if err != nil {
			tx.Rollback()
			return err
		}

		err = tx.Commit()
		if err != nil {
			return err
		}
		log.Printf("[Auth] Auto-accepted pending invitation %s to workspace %s for user %s (%s)", inv.id, inv.workspaceID, userID, email)
	}

	return nil
}
