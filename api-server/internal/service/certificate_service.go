package service

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	mathrand "math/rand"
	"strings"
	"time"

	"github.com/CzarSimon/httputil"
	"github.com/CzarSimon/httputil/crypto"
	"github.com/CzarSimon/httputil/id"
	"github.com/CzarSimon/httputil/jwt"
	"github.com/CzarSimon/webca/api-server/internal/audit"
	"github.com/CzarSimon/webca/api-server/internal/authorization"
	"github.com/CzarSimon/webca/api-server/internal/model"
	"github.com/CzarSimon/webca/api-server/internal/password"
	"github.com/CzarSimon/webca/api-server/internal/repository"
	"github.com/CzarSimon/webca/api-server/internal/rsautil"
	"github.com/CzarSimon/webca/api-server/internal/timeutil"
	"github.com/opentracing/opentracing-go"
)

const (
	textPlainType   = "text/plain"
	maxSerialNumber = 9007199254740991
)

func init() {
	mathrand.Seed(time.Now().UnixNano())
}

// CertificateService service responsible for certificate createion and management.
type CertificateService struct {
	AuditLog        audit.Logger
	CertRepo        repository.CertificateRepository
	KeyPairRepo     repository.KeyPairRepository
	UserRepo        repository.UserRepository
	PasswordService *password.Service
	AuthService     *authorization.Service
}

// GetCertificate retrieves certificate if it exists.
func (c *CertificateService) GetCertificate(ctx context.Context, principal jwt.User, id string) (model.Certificate, error) {
	span, ctx := opentracing.StartSpanFromContext(ctx, "certificate_service_get_certificate")
	defer span.Finish()

	cert, err := c.findCertificate(ctx, principal, id)
	if err != nil {
		return model.Certificate{}, err
	}

	c.logCertificateReading(ctx, cert, principal.ID)
	return cert, nil
}

// GetCertificates retrieves a list of certificates based on account id.
func (c *CertificateService) GetCertificates(ctx context.Context, principal jwt.User, filter model.CertificateFilter) (model.CertificatePage, error) {
	span, ctx := opentracing.StartSpanFromContext(ctx, "certificate_service_get_certificates")
	defer span.Finish()

	err := c.AuthService.AssertAccountAccess(ctx, principal, filter.AccountID)
	if err != nil {
		return model.CertificatePage{}, err
	}

	certs, err := c.getCertificatesByFilter(ctx, filter)
	if err != nil {
		return model.CertificatePage{}, err
	}

	go c.logCertificatesReading(context.Background(), certs, principal.ID)
	rlen := len(certs)
	return model.CertificatePage{
		CurrentPage:    1,
		TotalPages:     1,
		TotalResults:   rlen,
		ResultsPerPage: rlen,
		Results:        certs,
	}, nil
}

func (c *CertificateService) getCertificatesByFilter(ctx context.Context, filter model.CertificateFilter) ([]model.Certificate, error) {
	if filter.Types == nil || len(filter.Types) == 0 {
		return c.CertRepo.FindByAccountID(ctx, filter.AccountID)
	}

	return c.CertRepo.FindByAccountIDAndTypes(ctx, filter.AccountID, filter.Types)
}

// GetOptions fetches certificate creation options.
func (c *CertificateService) GetOptions(ctx context.Context) (model.CertificateOptions, error) {
	span, ctx := opentracing.StartSpanFromContext(ctx, "certificate_service_get_options")
	defer span.Finish()

	types, err := c.CertRepo.FindTypes(ctx)
	if err != nil {
		return model.CertificateOptions{}, err
	}

	opts := model.CertificateOptions{
		Types:      types,
		Algorithms: []string{rsautil.Algorithm},
		Formats:    []string{"PEM"},
	}
	return opts, nil
}

// GetCertificateBody retrieves certificate body as an attachment.
func (c *CertificateService) GetCertificateBody(ctx context.Context, principal jwt.User, id string, fullchain bool) (model.Attachment, error) {
	span, ctx := opentracing.StartSpanFromContext(ctx, "certificate_service_get_certificate_body")
	defer span.Finish()

	if fullchain {
		return c.getCertificateChainBody(ctx, principal, id)
	}

	return c.getCertificateBody(ctx, principal, id)
}

// GetCertificatePrivateKey retrieves certificate private key as an attachment.
func (c *CertificateService) GetCertificatePrivateKey(ctx context.Context, principal jwt.User, id, password string) (model.Attachment, error) {
	span, ctx := opentracing.StartSpanFromContext(ctx, "certificate_service_get_certificate_private_key")
	defer span.Finish()

	cert, err := c.findCertificate(ctx, principal, id)
	if err != nil {
		return model.Attachment{}, err
	}

	encryptedKeyPair, found, err := c.findCertificateKeyPair(ctx, principal, id)
	if err != nil {
		return model.Attachment{}, err
	}

	if !found {
		err = fmt.Errorf("KeyPair does not exist for certificate with id = %s", id)
		return model.Attachment{}, httputil.NotFoundError(err)
	}

	err = c.PasswordService.Verify(ctx, encryptedKeyPair.Credentials, password)
	if err != nil {
		return model.Attachment{}, err
	}

	keyPair, err := c.decryptKeys(ctx, encryptedKeyPair, password)
	if err != nil {
		return model.Attachment{}, err
	}

	c.logPrivateKeyReading(ctx, keyPair, principal.ID)
	return model.Attachment{
		Body:        keyPair.PrivateKey,
		ContentType: textPlainType,
		Filename:    attachmentFilename(cert.Name, "private-key", keyPair.Format),
	}, err
}

// Create creates and stores a certificate and private key.
func (c *CertificateService) Create(ctx context.Context, req model.CertificateRequest) (model.Certificate, error) {
	span, ctx := opentracing.StartSpanFromContext(ctx, "certificate_service_create")
	defer span.Finish()

	err := c.PasswordService.Allowed(req.Password)
	if err != nil {
		return model.Certificate{}, httputil.BadRequestError(err)
	}

	user, err := c.findUser(ctx, req.UserID)
	if err != nil {
		return model.Certificate{}, err
	}

	keys, err := c.createKeys(ctx, req.KeyRequest())
	if err != nil {
		return model.Certificate{}, err
	}

	cert, err := c.createCertificate(ctx, req, keys, user)
	if err != nil {
		return model.Certificate{}, err
	}

	return cert, nil
}

func (c *CertificateService) createCertificate(ctx context.Context, req model.CertificateRequest, keys model.KeyEncoder, user model.User) (model.Certificate, error) {
	keyPair, err := c.encryptKeys(ctx, keys.Encode(), req.Password, user)
	if err != nil {
		return model.Certificate{}, err
	}

	signingKeys, err := c.getSigningKeys(ctx, req, keys, user)
	if err != nil {
		return model.Certificate{}, err
	}

	cert, err := signCertificate(assembleCertificate(req, keyPair, user), keys.PublicKey(), signingKeys.PrivateKey())
	if err != nil {
		return model.Certificate{}, err
	}

	err = c.CertRepo.Save(ctx, cert)
	if err != nil {
		return model.Certificate{}, err
	}

	c.logNewCertificate(ctx, cert, user.ID)
	return cert, nil
}

func (c *CertificateService) createKeys(ctx context.Context, req model.KeyRequest) (model.KeyEncoder, error) {
	switch req.Algorithm {
	case rsautil.Algorithm:
		return rsautil.GenerateKeys(req)
	default:
		err := fmt.Errorf("CertificateService: unsupported KeyRequest.Algorithm=%s", req.Algorithm)
		return nil, httputil.BadRequestError(err)
	}
}

func (c *CertificateService) getCertificateBody(ctx context.Context, principal jwt.User, id string) (model.Attachment, error) {
	cert, err := c.findCertificate(ctx, principal, id)
	if err != nil {
		return model.Attachment{}, err
	}

	c.logCertificateBodyReading(ctx, cert, principal.ID)
	return model.Attachment{
		Body:        cert.Body,
		ContentType: textPlainType,
		Filename:    attachmentFilename(cert.Name, cert.Type, cert.Format),
	}, err
}

func (c *CertificateService) getCertificateChainBody(ctx context.Context, principal jwt.User, id string) (model.Attachment, error) {
	chain, err := c.getCertificateChain(ctx, principal, id)
	if err != nil {
		return model.Attachment{}, err
	}

	if len(chain) < 1 {
		err = fmt.Errorf("no certificate chain exits for certificate(id=%s)", id)
		return model.Attachment{}, httputil.BadRequestError(err)
	}

	body := ""
	for _, cert := range chain {
		body += cert.Body
		c.logCertificateBodyReading(ctx, cert, principal.ID)
	}

	return model.Attachment{
		Body:        body,
		ContentType: textPlainType,
		Filename:    attachmentFilename(chain[0].Name, "fullchain", chain[0].Format),
	}, err
}

func (c *CertificateService) getCertificateChain(ctx context.Context, principal jwt.User, id string) ([]model.Certificate, error) {
	certificates := make([]model.Certificate, 0)
	certID := id
	for certID != "" {
		cert, err := c.findCertificate(ctx, principal, certID)
		if err != nil {
			return nil, err
		}

		if cert.Type == model.RootCAType {
			break
		}

		certificates = append(certificates, cert)
		certID = cert.SignatoryID
	}

	return certificates, nil
}

func (c *CertificateService) getSigningKeys(ctx context.Context, req model.CertificateRequest, certKeys model.KeyEncoder, user model.User) (model.KeyEncoder, error) {
	if req.Type == model.RootCAType {
		return certKeys, nil
	}

	encryptedKeys, err := c.findSigningKeyPair(ctx, user.JWTUser(), req.Signatory.ID)
	if err != nil {
		return nil, err
	}

	keyPair, err := c.decryptKeys(ctx, encryptedKeys, req.Signatory.Password)
	if err != nil {
		return nil, err
	}

	keys, err := rsautil.Decode(keyPair)
	if err != nil {
		return nil, err
	}

	return keys, nil
}

func (c *CertificateService) findSigningKeyPair(ctx context.Context, principal jwt.User, certificateID string) (model.KeyPair, error) {
	cert, found, err := c.CertRepo.Find(ctx, certificateID)
	if err != nil {
		return model.KeyPair{}, err
	}

	if !found {
		err = fmt.Errorf("certificate with id %s does not exist", certificateID)
		return model.KeyPair{}, httputil.PreconditionRequiredError(err)
	}

	err = c.AuthService.AssertAccountAccess(ctx, principal, cert.AccountID)
	if err != nil {
		return model.KeyPair{}, err
	}

	if cert.Type != model.RootCAType && cert.Type != model.IntermediateCAType {
		err := fmt.Errorf("invalid signing certificate: %s", cert)
		return model.KeyPair{}, httputil.BadRequestError(err)
	}

	keyPair, found, err := c.findCertificateKeyPair(ctx, principal, certificateID)
	if err != nil {
		return model.KeyPair{}, err
	}

	if !found {
		err = fmt.Errorf("KeyPair does not exist for certificate with id = %s", certificateID)
		return model.KeyPair{}, httputil.PreconditionRequiredError(err)
	}

	return keyPair, nil
}

func (c *CertificateService) encryptKeys(ctx context.Context, keyPair model.KeyPair, pwd string, user model.User) (model.KeyPair, error) {
	creds, err := c.PasswordService.Hash(ctx, pwd)
	if err != nil {
		return model.KeyPair{}, err
	}

	salt, err := c.PasswordService.GenerateSalt()
	if err != nil {
		return model.KeyPair{}, fmt.Errorf("salt generation failed: %w", err)
	}

	encryptionKey, err := crypto.Hmac([]byte(pwd), salt)
	if err != nil {
		return model.KeyPair{}, fmt.Errorf("failed to generate encryption key: %w", err)
	}

	ciphertext, err := crypto.NewAESCipher(encryptionKey).Encrypt([]byte(keyPair.PrivateKey))
	if err != nil {
		return model.KeyPair{}, fmt.Errorf("failed to encrypt private key: %w", err)
	}

	keyPair.Credentials = creds
	keyPair.EncryptionSalt = base64.StdEncoding.EncodeToString(salt)
	keyPair.PrivateKey = base64.StdEncoding.EncodeToString(ciphertext)
	keyPair.AccountID = user.Account.ID

	return keyPair, nil
}

func (c *CertificateService) decryptKeys(ctx context.Context, keyPair model.KeyPair, pwd string) (model.KeyPair, error) {
	salt, err := base64.StdEncoding.DecodeString(keyPair.EncryptionSalt)
	if err != nil {
		return model.KeyPair{}, fmt.Errorf("failed to decode salt: %w", err)
	}

	ciphertext, err := base64.StdEncoding.DecodeString(keyPair.PrivateKey)
	if err != nil {
		return model.KeyPair{}, fmt.Errorf("failed to decode encrypted private key: %w", err)
	}

	encryptionKey, err := crypto.Hmac([]byte(pwd), salt)
	if err != nil {
		return model.KeyPair{}, fmt.Errorf("failed to generate encryption key: %w", err)
	}

	plaintext, err := crypto.NewAESCipher(encryptionKey).Decrypt(ciphertext)
	if err != nil {
		return model.KeyPair{}, fmt.Errorf("failed to decrypt private key: %w", err)
	}

	keyPair.PrivateKey = string(plaintext)
	return keyPair, nil
}

func (c *CertificateService) findCertificate(ctx context.Context, principal jwt.User, id string) (model.Certificate, error) {
	cert, found, err := c.CertRepo.Find(ctx, id)
	if err != nil {
		return model.Certificate{}, err
	}

	if !found {
		err = fmt.Errorf("certificate with id %s does not exist", id)
		return model.Certificate{}, httputil.NotFoundError(err)
	}

	err = c.AuthService.AssertAccountAccess(ctx, principal, cert.AccountID)
	if err != nil {
		return model.Certificate{}, err
	}

	return cert, nil
}

func (c *CertificateService) findCertificateKeyPair(ctx context.Context, principal jwt.User, id string) (model.KeyPair, bool, error) {
	keyPair, found, err := c.KeyPairRepo.FindByCertificateID(ctx, id)
	if err != nil || !found {
		return model.KeyPair{}, found, err
	}

	err = c.AuthService.AssertAccountAccess(ctx, principal, keyPair.AccountID)
	if err != nil {
		return model.KeyPair{}, true, err
	}

	return keyPair, true, nil
}

func (c *CertificateService) findUser(ctx context.Context, userID string) (model.User, error) {
	user, exists, err := c.UserRepo.Find(ctx, userID)
	if err != nil {
		return model.User{}, httputil.InternalServerError(err)
	}

	if !exists {
		err := fmt.Errorf("unable to find User(id=%s) even though an authenticated user id was provided", userID)
		return model.User{}, httputil.UnauthorizedError(err)
	}

	return user, nil
}

func (c *CertificateService) logNewCertificate(ctx context.Context, cert model.Certificate, userID string) {
	c.AuditLog.Create(ctx, userID, "certificate:%s", cert.ID)
	c.AuditLog.Create(ctx, userID, "key-pair:%s", cert.KeyPair.ID)
}

func (c *CertificateService) logCertificateReading(ctx context.Context, cert model.Certificate, userID string) {
	c.AuditLog.Read(ctx, userID, "certificate:%s", cert.ID)
}

func (c *CertificateService) logCertificatesReading(ctx context.Context, certs []model.Certificate, userID string) {
	for _, cert := range certs {
		c.logCertificateReading(ctx, cert, userID)
	}
}

func (c *CertificateService) logCertificateBodyReading(ctx context.Context, cert model.Certificate, userID string) {
	c.AuditLog.Read(ctx, userID, "certificate:%s:body", cert.ID)
}

func (c *CertificateService) logPrivateKeyReading(ctx context.Context, keyPair model.KeyPair, userID string) {
	c.AuditLog.Read(ctx, userID, "key-pair:%s:private-key", keyPair.ID)
}

func signCertificate(cert model.Certificate, pub, priv interface{}) (model.Certificate, error) {
	template, err := x509Template(cert)
	if err != nil {
		return model.Certificate{}, err
	}

	b, err := x509.CreateCertificate(rand.Reader, template, template, pub, priv)
	if err != nil {
		return model.Certificate{}, fmt.Errorf("failed to create x509 certificate: %w", err)
	}

	block := &pem.Block{Type: "CERTIFICATE", Bytes: b}
	cert.Body = string(pem.EncodeToMemory(block))
	return cert, nil
}

func assembleCertificate(req model.CertificateRequest, keyPair model.KeyPair, user model.User) model.Certificate {
	now := timeutil.Now()

	return model.Certificate{
		ID:           id.New(),
		Name:         req.Name,
		SerialNumber: randomSerialNumber(),
		Subject:      req.Subject,
		KeyPair:      keyPair,
		Format:       keyPair.Format,
		Type:         req.Type,
		SignatoryID:  req.Signatory.ID,
		AccountID:    user.Account.ID,
		CreatedAt:    now,
		ExpiresAt:    now.AddDate(0, 0, req.ExpiresInDays),
	}
}

func x509Template(cert model.Certificate) (*x509.Certificate, error) {
	c := &x509.Certificate{
		SerialNumber: big.NewInt(cert.SerialNumber),
		Subject: pkix.Name{
			CommonName:         cert.Subject.CommonName,
			Country:            []string{cert.Subject.Country},
			Province:           []string{cert.Subject.State},
			Locality:           []string{cert.Subject.Locality},
			Organization:       []string{cert.Subject.Organization},
			OrganizationalUnit: []string{cert.Subject.OrganizationalUnit},
		},
		NotBefore: cert.CreatedAt,
		NotAfter:  cert.ExpiresAt,
	}

	switch cert.Type {
	case model.RootCAType:
	case model.IntermediateCAType:
		c.IsCA = true
		c.KeyUsage = x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign
		c.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}
		c.BasicConstraintsValid = true
	case model.UserCertificateType:
		c.IsCA = false
		c.KeyUsage = x509.KeyUsageDigitalSignature
		c.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}
	default:
		err := fmt.Errorf("unsupported certificate type: %s", cert.Type)
		return nil, httputil.BadRequestError(err)
	}

	return c, nil
}

func attachmentFilename(name, category, format string) string {
	filename := strings.ToLower(fmt.Sprintf("%s.%s.%s", name, category, format))
	replacements := []string{"_", " "}
	for _, r := range replacements {
		filename = strings.ReplaceAll(filename, r, "-")
	}
	return filename
}

func randomSerialNumber() int64 {
	number := mathrand.Int63n(maxSerialNumber-1) + 1
	if number < 0 {
		return number * -1
	}

	return number
}
