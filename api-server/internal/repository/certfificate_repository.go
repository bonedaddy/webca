package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/CzarSimon/httputil/dbutil"
	"github.com/CzarSimon/webca/api-server/internal/model"
	"github.com/opentracing/opentracing-go"
)

// CertificateRepository data access layer for certificates.
type CertificateRepository interface {
	Save(ctx context.Context, cert model.Certificate) error
	Find(ctx context.Context, id string) (model.Certificate, bool, error)
	FindByNameAndAccountID(ctx context.Context, name, accountID string) (model.Certificate, bool, error)
	FindByAccountID(ctx context.Context, accountID string) ([]model.Certificate, error)
	FindByAccountIDAndTypes(ctx context.Context, accountID string, types []string) ([]model.Certificate, error)
	FindTypes(ctx context.Context) ([]model.CertificateType, error)
}

// NewCertificateRepository creates an CertificateRepository using the default implementation.
func NewCertificateRepository(db *sql.DB) CertificateRepository {
	return &certRepo{
		db: db,
	}
}

type certRepo struct {
	db *sql.DB
}

const saveCertificateQuery = `
	INSERT INTO certificate(id, name, serial_number, subject, body, format, type, key_pair_id, signatory_id, account_id, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

func (r *certRepo) Save(ctx context.Context, cert model.Certificate) error {
	span, _ := opentracing.StartSpanFromContext(ctx, "key_pair_repo_save")
	defer span.Finish()

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to start transtaction: %w", err)
	}

	key := cert.KeyPair
	_, err = tx.ExecContext(ctx, saveKeyPairQuery,
		key.ID, key.PublicKey, key.PrivateKey, key.Format, key.Algorithm, key.EncryptionSalt,
		key.Credentials.Password, key.Credentials.Salt, key.AccountID, key.CreatedAt,
	)
	if err != nil {
		dbutil.Rollback(tx)
		return fmt.Errorf("failed to insert %s: %w", key, err)
	}

	sigID := sql.NullString{
		String: cert.SignatoryID,
		Valid:  cert.SignatoryID != "",
	}
	_, err = tx.ExecContext(ctx, saveCertificateQuery,
		cert.ID, cert.Name, cert.SerialNumber, cert.Subject.String(), cert.Body, cert.Format, cert.Type, cert.KeyPair.ID, sigID, cert.AccountID, cert.CreatedAt, cert.ExpiresAt,
	)
	if err != nil {
		dbutil.Rollback(tx)
		return fmt.Errorf("failed to insert %s: %w", cert, err)
	}

	return tx.Commit()
}

const findCertificateQuery = `
	SELECT 
		id, 
		name,
		serial_number,
		body,
		format,
		type,
		signatory_id,
		account_id,
		created_at,
		expires_at
	FROM 
		certificate
	WHERE
		id = ?`

func (r *certRepo) Find(ctx context.Context, id string) (model.Certificate, bool, error) {
	span, ctx := opentracing.StartSpanFromContext(ctx, "cert_repo_find_by_name_and_account_id")
	defer span.Finish()

	var c model.Certificate
	sigID := sql.NullString{}
	err := r.db.QueryRowContext(ctx, findCertificateQuery, id).Scan(
		&c.ID, &c.Name, &c.SerialNumber, &c.Body, &c.Format, &c.Type, &sigID, &c.AccountID, &c.CreatedAt, &c.ExpiresAt,
	)
	if err == sql.ErrNoRows {
		return model.Certificate{}, false, nil
	}
	if err != nil {
		return model.Certificate{}, false, fmt.Errorf("failed to query certificate(id=%s): %w", id, err)
	}

	c.SignatoryID = sigID.String
	return c, true, nil
}

const findCertificateByNameAndAccountIDQuery = `
	SELECT 
		id, 
		name,
		serial_number,
		body,
		format,
		type,
		key_pair_id,
		signatory_id,
		account_id,
		created_at,
		expires_at
	FROM 
		certificate
	WHERE
		name = ?
		AND account_id = ?`

func (r *certRepo) FindByNameAndAccountID(ctx context.Context, name, accountID string) (model.Certificate, bool, error) {
	span, ctx := opentracing.StartSpanFromContext(ctx, "cert_repo_find_by_name_and_account_id")
	defer span.Finish()

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Certificate{}, false, fmt.Errorf("failed to start transtaction: %w", err)
	}

	var c model.Certificate
	var keyPairID string
	sigID := sql.NullString{}
	err = tx.QueryRowContext(ctx, findCertificateByNameAndAccountIDQuery, name, accountID).Scan(
		&c.ID, &c.Name, &c.SerialNumber, &c.Body, &c.Format, &c.Type, &keyPairID, &sigID, &c.AccountID, &c.CreatedAt, &c.ExpiresAt,
	)
	if err == sql.ErrNoRows {
		dbutil.Rollback(tx)
		return model.Certificate{}, false, nil
	}
	if err != nil {
		dbutil.Rollback(tx)
		return model.Certificate{}, false, fmt.Errorf("failed to query certificate(name=%s, account_id=%s): %w", name, accountID, err)
	}

	keyPair, found, err := findKeyPair(ctx, tx, keyPairID)
	if !found || err != nil {
		dbutil.Rollback(tx)
		return model.Certificate{}, found, err
	}

	c.KeyPair = keyPair
	c.SignatoryID = sigID.String
	return c, true, tx.Commit()
}

const findKeyPairsQuery = `
	SELECT 
		id, 
		public_key,
		private_key,
		format,
		type,
		encryption_salt,
		password,
		password_salt,
		account_id,
		created_at
	FROM 
		key_pair
	WHERE
		id = ?`

func findKeyPair(ctx context.Context, tx *sql.Tx, id string) (model.KeyPair, bool, error) {
	span, ctx := opentracing.StartSpanFromContext(ctx, "repository_find_key_pair")
	defer span.Finish()

	var k model.KeyPair
	err := tx.QueryRowContext(ctx, findKeyPairsQuery, id).Scan(
		&k.ID, &k.PublicKey, &k.PrivateKey, &k.Format, &k.Algorithm, &k.EncryptionSalt, &k.Credentials.Password, &k.Credentials.Salt, &k.AccountID, &k.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return model.KeyPair{}, false, nil
	}
	if err != nil {
		return model.KeyPair{}, false, fmt.Errorf("failed to query key_pair(id=%s): %w", id, err)
	}

	return k, true, nil
}

const findCertificatesByAccountIDQuery = `
	SELECT 
		id, 
		name,
		serial_number,
		body,
		format,
		type,
		signatory_id,
		account_id,
		created_at,
		expires_at
	FROM 
		certificate
	WHERE
		account_id = ?`

func (r *certRepo) FindByAccountID(ctx context.Context, accountID string) ([]model.Certificate, error) {
	span, ctx := opentracing.StartSpanFromContext(ctx, "cert_repo_find_by_account_id")
	defer span.Finish()

	rows, err := r.db.QueryContext(ctx, findCertificatesByAccountIDQuery, accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to query certificate by accountId=%s: %w", accountID, err)
	}
	defer rows.Close()

	return mapRowsToCertificates(rows)
}

const findCertificatesByAccountIDAndTypesQuery = `
	SELECT 
		id, 
		name,
		serial_number,
		body,
		format,
		type,
		signatory_id,
		account_id,
		created_at,
		expires_at
	FROM 
		certificate
	WHERE
		account_id = ?
		AND type IN (?%s)`

func (r *certRepo) FindByAccountIDAndTypes(ctx context.Context, accountID string, types []string) ([]model.Certificate, error) {
	span, ctx := opentracing.StartSpanFromContext(ctx, "cert_repo_find_by_account_id_and_types")
	defer span.Finish()

	query := fmt.Sprintf(findCertificatesByAccountIDAndTypesQuery, strings.Repeat(", ?", len(types)-1))
	args := createAccountIDAndTypesArgs(accountID, types)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query certificate by accountId=%s and types=%v: %w", accountID, types, err)
	}
	defer rows.Close()

	return mapRowsToCertificates(rows)
}

func createAccountIDAndTypesArgs(accountID string, types []string) []interface{} {
	args := make([]interface{}, 1, len(types)+1)
	args[0] = accountID
	for _, t := range types {
		args = append(args, t)
	}

	return args
}

const findCertificateTypesQuery = `
	SELECT 
		name,
		active,
		created_at,
		updated_at
	FROM 
		certificate_type
	WHERE
		active = 1`

func (r *certRepo) FindTypes(ctx context.Context) ([]model.CertificateType, error) {
	span, ctx := opentracing.StartSpanFromContext(ctx, "cert_repo_find_types")
	defer span.Finish()

	types := make([]model.CertificateType, 0)
	rows, err := r.db.QueryContext(ctx, findCertificateTypesQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to query certificate_type where active=1: %w", err)
	}
	defer rows.Close()

	var t model.CertificateType
	for rows.Next() {
		err = rows.Scan(&t.Name, &t.Active, &t.CreatedAt, &t.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row for certificate_type where active=1: %w", err)
		}
		types = append(types, t)
	}

	return types, nil
}

func mapRowsToCertificates(rows *sql.Rows) ([]model.Certificate, error) {
	certs := make([]model.Certificate, 0)

	var c model.Certificate
	sigID := sql.NullString{}
	for rows.Next() {
		err := rows.Scan(&c.ID, &c.Name, &c.SerialNumber, &c.Body, &c.Format, &c.Type, &sigID, &c.AccountID, &c.CreatedAt, &c.ExpiresAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row for certificate. %w", err)
		}
		c.SignatoryID = sigID.String
		certs = append(certs, c)
	}

	return certs, nil
}
