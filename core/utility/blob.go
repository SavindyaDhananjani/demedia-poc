// Copyright 2020 The Moov Authors
// Use of this source code is governed by an Apache License
// license that can be found in the LICENSE file.

package utility

import (
	"context"
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"io"

	"github.com/moov-io/cryptfs"

	"gocloud.dev/blob"
	_ "gocloud.dev/blob/azureblob"
	_ "gocloud.dev/blob/fileblob"
	_ "gocloud.dev/blob/gcsblob"
	_ "gocloud.dev/blob/memblob"
	_ "gocloud.dev/blob/s3blob"
)

type AuditTrail struct {
	ID        string
	BucketURI string
	GPG       *GPG
}

func (cfg *AuditTrail) Validate() error {
	if cfg == nil {
		return nil
	}
	if cfg.BucketURI == "" {
		return errors.New("missing bucket_uri")
	}
	return nil
}

type GPG struct {
	KeyFile string
	Signer  *Signer
}

type Signer struct {
	KeyFile     string
	KeyPassword string
}

// blobStorage implements Storage with gocloud.dev/blob which allows
// clients to use AWS S3, GCP Storage, and Azure Storage.
type blobStorage struct {
	id      string
	bucket  *blob.Bucket
	cryptor *cryptfs.FS
}

func NewBlobStorage(cfg *AuditTrail) (*blobStorage, error) {
	storage := &blobStorage{id: cfg.ID}

	bucket, err := blob.OpenBucket(context.Background(), cfg.BucketURI)
	if err != nil {
		return nil, err
	}
	storage.bucket = bucket

	if cfg.GPG != nil {
		storage.cryptor, err = cryptfs.FromCryptor(cryptfs.NewGPGEncryptorFile(cfg.GPG.KeyFile))
		if err != nil {
			return nil, err
		}
	}

	return storage, nil
}

func (bs *blobStorage) Close() error {
	if bs == nil {
		return nil
	}
	return bs.bucket.Close()
}

func (bs *blobStorage) SaveFile(filepath string, data []byte) error {
	var encrypted []byte
	var err error
	if bs.cryptor != nil {
		encrypted, err = bs.cryptor.Disfigure(data)
	} else {
		encrypted = data
	}
	if err != nil {
		return err
	}

	exists, err := bs.bucket.Exists(context.Background(), filepath)
	if exists {
		return nil
	}
	if err != nil {
		return err
	}

	before := func(asFunc func(interface{}) bool) error {
		s3_req := &s3manager.UploadInput{}
		ok := asFunc(&s3_req)
		if ok {
			s3_req.ACL = aws.String("public-read")
		}

		return nil
	}

	wr_opts := &blob.WriterOptions{
		BeforeWrite: before,
	}

	w, err := bs.bucket.NewWriter(context.Background(), filepath, wr_opts)
	if err != nil {
		return err
	}

	_, copyErr := w.Write(encrypted)
	closeErr := w.Close()

	if copyErr != nil || closeErr != nil {
		return fmt.Errorf("copyErr=%v closeErr=%v", copyErr, closeErr)
	}

	return nil
}

func (bs *blobStorage) GetFile(filepath string) (io.ReadCloser, error) {
	r, err := bs.bucket.NewReader(context.Background(), filepath, nil)
	if err != nil {
		return nil, fmt.Errorf("get file: %v", err)
	}
	return r, nil
}

func (bs *blobStorage) Delete(filepath string) error {
	err := bs.bucket.Delete(context.Background(), filepath)
	if err != nil {
		return err
	}

	return nil
}

func (bs *blobStorage) GetFileURL(filepath string) (string, error) {
	return bs.bucket.SignedURL(context.Background(), filepath, nil)
}
