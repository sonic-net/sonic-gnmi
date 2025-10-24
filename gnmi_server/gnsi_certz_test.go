package gnmi

import (
	"context"
	//"crypto/tls"
	//"crypto/x509"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	//"sync"
	"testing"
	"time"

	lvl "github.com/sonic-net/sonic-gnmi/gnmi_server/log"

	log "github.com/golang/glog"
	certz "github.com/openconfig/gnsi/certz"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	//"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

const (
	mtlsTestDir = "../testdata/mtls"
	// CACert is a test Certificate Authority Certificate (trustBundle)
	CACert = mtlsTestDir + "/ca.lnk"
	// SKey is a test Server Key that is used by gRPC and monitored for changes.
	SKey = mtlsTestDir + "/server_key.lnk"
	// SCert is a test Server Certificate that is used by gRPC and monitored for changes.
	SCert = mtlsTestDir + "/server_cert.lnk"
	// CACert is a test Certificate Authority Certificate
	CACertV1     = mtlsTestDir + "/ca_V1_bundle.pem"
	GoldCACertV1 = mtlsTestDir + "/gold/ca.pem"
	// SKeyV1 is the first test Server Key
	SKeyV1     = mtlsTestDir + "/server_V1_key.pem"
	GoldSKeyV1 = mtlsTestDir + "/gold/server_1_key.pem"
	// SCertV1 is the first test Server Certificate
	SCertV1     = mtlsTestDir + "/server_V1_cert.pem"
	GoldSCertV1 = mtlsTestDir + "/gold/server_1_cert.pem"
	// SKeyV2 is the second test Server Key
	SKeyV2     = mtlsTestDir + "/server_V2_key.pem"
	GoldSKeyV2 = mtlsTestDir + "/gold/server_2_key.pem"
	// SCertV2 is the second test Server Certificate
	SCertV2     = mtlsTestDir + "/server_V2_cert.pem"
	GoldSCertV2 = mtlsTestDir + "/gold/server_2_cert.pem"
	// CKey is the first test Client Key
	CKey = mtlsTestDir + "/client_1_key.pem"
	// CCert is the first test Client Certificate
	CCert = mtlsTestDir + "/client_1_cert.pem"
	// CRL files
	GoldCRLFile       = "039bd53b.r0"
	GoldCRLPath       = mtlsTestDir + "/gold"
	crlConfigTestPath = mtlsTestDir + "/crls"
	SrvTestKeyLink    = mtlsTestDir + "/server_key.lnk"
	SrvTestCertLink   = mtlsTestDir + "/server_cert.lnk"
)

/*
	func createMTLSServer(t *testing.T, caCert string) *Server {
		//cfg := testServerConfig(certSrvType)
		cfg := &Config
		cfg.CaCertLnk = CACert
		cfg.CaCertFile = caCert
		cfg.SrvCertFile = SCertV1
		cfg.SrvKeyFile = SKeyV1
		cfg.CertzMetaFile = certzTestMetaFile
		cfg.CertCRLConfig = crlConfigTestPath
		cfg.GetOptions = SrvAdvConfig
		if err := resetSrvCertKeyToV1(cfg); err != nil {
			t.Fatalf("couldn't reset server certificate/key: %v", err)
		}

		s := createCustomServer(t, cfg)
		return s
	}
*/
var gnsiCertzTestCases = []struct {
	desc string
	f    func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server)
}{
	{
		desc: "RotateCertificateDefaultSuccess",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err)
			}

			// 1) Generate a Certificate and send it to the switch.
			ver := generateVersion()
			certPem, err := os.ReadFile(GoldSCertV2)
			if err != nil {
				t.Fatal(err)
			}
			keyPem, err := os.ReadFile(GoldSKeyV2)
			if err != nil {
				t.Fatal(err)
			}
			err = stream.Send(&certz.RotateCertificateRequest{
				RotateRequest: &certz.RotateCertificateRequest_Certificates{
					Certificates: &certz.UploadRequest{
						Entities: []*certz.Entity{
							{
								Version:   ver,
								CreatedOn: 123,
								Entity: &certz.Entity_CertificateChain{
									CertificateChain: &certz.CertificateChain{
										Certificate: &certz.Certificate{
											Type:        certz.CertificateType_CERTIFICATE_TYPE_X509,
											Encoding:    certz.CertificateEncoding_CERTIFICATE_ENCODING_PEM,
											Certificate: certPem,
											PrivateKey:  keyPem,
										},
									},
								},
							},
						},
					},
				},
			})
			if err != nil {
				t.Fatal(err)
			}

			// 2) Receive confirmation that the certificate was accepted.
			resp, err := stream.Recv()
			if err != nil {
				t.Fatal(err)
			}
			bundle := resp.GetCertificates()
			if bundle == nil {
				t.Fatal("Did not receive expected UploadResponse response")
			}

			// 3) Verify that a connection can be established after the rotation.
			//    This connection should be established and then rejected due to two Rotation() calls in parallel.
			check, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err)
			}
			_, err = check.Recv()
			if err == nil {
				t.Fatal("Expected an error")
			}
			if err != nil && status.Code(err) != codes.Aborted {
				t.Fatalf("Unexpected error: %v", err)
			}

			// 4) Finalize the operation by sending the Finalize message.
			err = stream.Send(&certz.RotateCertificateRequest{
				RotateRequest: &certz.RotateCertificateRequest_FinalizeRotation{},
			})
			if err != nil {
				t.Fatal(err)
			}

			// 5) Close the connection.
			stream.CloseSend()

			// 6) Receive error reporting premature closure of the stream.
			_, err = stream.Recv()
			if err != io.EOF {
				t.Fatalf("Expected an error reporting closure of the stream but got: %v", err)
			}

			isLinkCorrect(t, s.config.SrvCertLnk, "cert", "gnxi", ver)
			isLinkCorrect(t, s.config.SrvKeyLnk, "key", "gnxi", ver)

		},
	},
	{
		desc: "RotateCRLDefaultSuccess",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err)
			}
			c, err := os.ReadDir(filepath.Join(s.config.CertCRLConfig, crlDefault))
			if err != nil {
				t.Error(err)
			}
			startCount := len(c)

			// 1) Generate a CRL and send it to the switch.
			crlPem, err := os.ReadFile(filepath.Join(GoldCRLPath, GoldCRLFile))
			if err != nil {
				t.Fatal(err)
			}
			version := generateVersion()
			err = stream.Send(&certz.RotateCertificateRequest{
				RotateRequest: &certz.RotateCertificateRequest_Certificates{
					Certificates: &certz.UploadRequest{
						Entities: []*certz.Entity{
							{
								Version:   version,
								CreatedOn: 123,
								Entity: &certz.Entity_CertificateRevocationListBundle{
									CertificateRevocationListBundle: &certz.CertificateRevocationListBundle{
										CertificateRevocationLists: []*certz.CertificateRevocationList{
											&certz.CertificateRevocationList{
												Type:                      certz.CertificateType_CERTIFICATE_TYPE_X509,
												Encoding:                  certz.CertificateEncoding_CERTIFICATE_ENCODING_PEM,
												CertificateRevocationList: crlPem,
												Id:                        GoldCRLFile,
											},
										},
									},
								},
							},
						},
					},
				},
			})
			if err != nil {
				t.Fatal(err)
			}

			// 2) Receive confirmation that the certificate was accepted.
			resp, err := stream.Recv()
			if err != nil {
				t.Error(err)
			}

			bundle := resp.GetCertificates()
			if bundle == nil {
				t.Error("Did not receive expected UploadResponse response")
			}

			if err := stream.Send(&certz.RotateCertificateRequest{
				RotateRequest: &certz.RotateCertificateRequest_FinalizeRotation{},
			}); err != nil {
				t.Error(err)
			}

			stream.CloseSend()

			// Check that one new file has been written to crl
			c, err = os.ReadDir(filepath.Join(s.config.CertCRLConfig, crlDefault))
			if err != nil {
				t.Error(err)
			}
			if len(c) != startCount+1 {
				t.Fatalf("Test failed: Expected %v crl and found %v", startCount+1, len(c))
			}
		},
	},
	{
		desc: "RotateTrustBundleSuccess",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err)
			}

			ver := generateVersion()
			certPem, err := os.ReadFile(GoldCACertV1)
			if err != nil {
				t.Fatal(err)
			}
			err = stream.Send(&certz.RotateCertificateRequest{
				RotateRequest: &certz.RotateCertificateRequest_Certificates{
					Certificates: &certz.UploadRequest{
						Entities: []*certz.Entity{
							{
								Version:   ver,
								CreatedOn: 123,
								Entity: &certz.Entity_TrustBundle{
									TrustBundle: &certz.CertificateChain{
										Certificate: &certz.Certificate{
											Type:        certz.CertificateType_CERTIFICATE_TYPE_X509,
											Encoding:    certz.CertificateEncoding_CERTIFICATE_ENCODING_PEM,
											Certificate: certPem,
										},
									},
								},
							},
						},
					},
				},
			})
			if err != nil {
				t.Fatal(err)
			}

			resp, err := stream.Recv()
			if err != nil {
				t.Fatal(err)
			}
			bundle := resp.GetCertificates()
			if bundle == nil {
				t.Fatal("Did not receive expected UploadResponse response")
			}

			err = stream.Send(&certz.RotateCertificateRequest{
				RotateRequest: &certz.RotateCertificateRequest_FinalizeRotation{},
			})
			if err != nil {
				t.Fatal(err)
			}

			stream.CloseSend()

			_, err = stream.Recv()
			if err != io.EOF {
				t.Fatalf("Expected an error reporting closure of the stream but got: %v", err)
			}

			isLinkCorrect(t, s.config.CaCertLnk, "bundle", "ca_gnxi", ver)
		},
	},
	{
		desc: "RotateEmptyRequest",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err)
			}

			err = stream.Send(&certz.RotateCertificateRequest{})
			if err != nil {
				t.Fatal(err)
			}

			if _, err = stream.Recv(); status.Code(err) != codes.Aborted {
				t.Fatal(err)
			}
		},
	},
	{
		desc: "RotateCertificateMissingCert",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err)
			}

			// 1) Generate a Certificate and send it to the switch.
			err = stream.Send(&certz.RotateCertificateRequest{
				RotateRequest: &certz.RotateCertificateRequest_Certificates{
					Certificates: &certz.UploadRequest{
						Entities: []*certz.Entity{
							{
								Version:   "test",
								CreatedOn: 123,
								Entity: &certz.Entity_CertificateChain{
									CertificateChain: &certz.CertificateChain{
										Certificate: &certz.Certificate{
											Type:     certz.CertificateType_CERTIFICATE_TYPE_X509,
											Encoding: certz.CertificateEncoding_CERTIFICATE_ENCODING_PEM,
											// Certificate: []byte(`cert`),
											PrivateKey: []byte(`key`),
										},
									},
								},
							},
						},
					},
				},
			})
			if err != nil {
				t.Fatal(err)
			}

			// 2) Receive confirmation that the certificate was accepted.
			if _, err = stream.Recv(); status.Code(err) != codes.Aborted {
				t.Fatal(err)
			}
		},
	},
	{
		desc: "RotateCertificateMissingKey",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err)
			}

			err = stream.Send(&certz.RotateCertificateRequest{
				RotateRequest: &certz.RotateCertificateRequest_Certificates{
					Certificates: &certz.UploadRequest{
						Entities: []*certz.Entity{
							{
								Version:   "test",
								CreatedOn: 123,
								Entity: &certz.Entity_CertificateChain{
									CertificateChain: &certz.CertificateChain{
										Certificate: &certz.Certificate{
											Type:        certz.CertificateType_CERTIFICATE_TYPE_X509,
											Encoding:    certz.CertificateEncoding_CERTIFICATE_ENCODING_PEM,
											Certificate: []byte(`cert`),
											// PrivateKey:  []byte(`key`),
										},
									},
								},
							},
						},
					},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			// 2) Receive confirmation that the certificate was accepted.
			if _, err = stream.Recv(); status.Code(err) != codes.Aborted {
				t.Fatal(err)
			}
		},
	},
	{
		desc: "RotateCertificateMissingType",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err)
			}

			err = stream.Send(&certz.RotateCertificateRequest{
				RotateRequest: &certz.RotateCertificateRequest_Certificates{
					Certificates: &certz.UploadRequest{
						Entities: []*certz.Entity{
							{
								Version:   "test",
								CreatedOn: 123,
								Entity: &certz.Entity_CertificateChain{
									CertificateChain: &certz.CertificateChain{
										Certificate: &certz.Certificate{
											Encoding:    certz.CertificateEncoding_CERTIFICATE_ENCODING_PEM,
											Certificate: []byte(`cert`),
											PrivateKey:  []byte(`key`),
										},
									},
								},
							},
						},
					},
				},
			})
			if err != nil {
				t.Fatal(err)
			}

			// 2) Receive confirmation that the certificate was accepted.
			if _, err = stream.Recv(); status.Code(err) != codes.Aborted {
				t.Fatal(err)
			}
		},
	},
	{
		desc: "RotateCertificateMissingEncoding",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err)
			}

			err = stream.Send(&certz.RotateCertificateRequest{
				RotateRequest: &certz.RotateCertificateRequest_Certificates{
					Certificates: &certz.UploadRequest{
						Entities: []*certz.Entity{
							{
								Version:   "test",
								CreatedOn: 123,
								Entity: &certz.Entity_CertificateChain{
									CertificateChain: &certz.CertificateChain{
										Certificate: &certz.Certificate{
											Type:        certz.CertificateType_CERTIFICATE_TYPE_X509,
											Certificate: []byte(`cert`),
											PrivateKey:  []byte(`key`),
										},
									},
								},
							},
						},
					},
				},
			})
			if err != nil {
				t.Fatal(err)
			}

			// 2) Receive confirmation that the certificate was accepted.
			if _, err = stream.Recv(); status.Code(err) != codes.Aborted {
				t.Fatal(err)
			}
		},
	},
	{
		desc: "RotateTrustBundleMissingCert",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err)
			}

			err = stream.Send(&certz.RotateCertificateRequest{
				RotateRequest: &certz.RotateCertificateRequest_Certificates{
					Certificates: &certz.UploadRequest{
						Entities: []*certz.Entity{
							{
								Version:   "test",
								CreatedOn: 123,
								Entity: &certz.Entity_TrustBundle{
									TrustBundle: &certz.CertificateChain{
										Certificate: &certz.Certificate{
											Type:     certz.CertificateType_CERTIFICATE_TYPE_X509,
											Encoding: certz.CertificateEncoding_CERTIFICATE_ENCODING_PEM,
										},
									},
								},
							},
						},
					},
				},
			})
			if err != nil {
				t.Fatal(err)
			}

			if _, err = stream.Recv(); status.Code(err) != codes.Aborted {
				t.Fatal(err)
			}
		},
	},
	{
		desc: "RotateTrustBundleMissingType",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err)
			}

			err = stream.Send(&certz.RotateCertificateRequest{
				RotateRequest: &certz.RotateCertificateRequest_Certificates{
					Certificates: &certz.UploadRequest{
						Entities: []*certz.Entity{
							{
								Version:   "test",
								CreatedOn: 123,
								Entity: &certz.Entity_TrustBundle{
									TrustBundle: &certz.CertificateChain{
										Certificate: &certz.Certificate{
											Encoding:    certz.CertificateEncoding_CERTIFICATE_ENCODING_PEM,
											Certificate: []byte(`test`),
										},
									},
								},
							},
						},
					},
				},
			})
			if err != nil {
				t.Fatal(err)
			}

			// 2) Receive confirmation that the certificate was accepted.
			if _, err = stream.Recv(); status.Code(err) != codes.Aborted {
				t.Fatal(err)
			}
		},
	},
	{
		desc: "RotateTrustBundleMissingEncoding",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err)
			}

			err = stream.Send(&certz.RotateCertificateRequest{
				RotateRequest: &certz.RotateCertificateRequest_Certificates{
					Certificates: &certz.UploadRequest{
						Entities: []*certz.Entity{
							{
								Version:   "test",
								CreatedOn: 123,
								Entity: &certz.Entity_TrustBundle{
									TrustBundle: &certz.CertificateChain{
										Certificate: &certz.Certificate{
											Type:        certz.CertificateType_CERTIFICATE_TYPE_X509,
											Certificate: []byte(`test`),
										},
									},
								},
							},
						},
					},
				},
			})
			if err != nil {
				t.Fatal(err)
			}

			// 2) Receive confirmation that the certificate was accepted.
			if _, err = stream.Recv(); status.Code(err) != codes.Aborted {
				t.Fatal(err)
			}
		},
	},
	{
		desc: "RotateCRLMissingCert",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err)
			}

			err = stream.Send(&certz.RotateCertificateRequest{
				RotateRequest: &certz.RotateCertificateRequest_Certificates{
					Certificates: &certz.UploadRequest{
						Entities: []*certz.Entity{
							{
								Version:   "test",
								CreatedOn: 123,
								Entity: &certz.Entity_CertificateRevocationListBundle{
									CertificateRevocationListBundle: &certz.CertificateRevocationListBundle{
										CertificateRevocationLists: []*certz.CertificateRevocationList{
											&certz.CertificateRevocationList{
												Type:     certz.CertificateType_CERTIFICATE_TYPE_X509,
												Encoding: certz.CertificateEncoding_CERTIFICATE_ENCODING_PEM,
											},
										},
									},
								},
							},
						},
					},
				},
			})
			if err != nil {
				t.Fatal(err)
			}

			// 2) Receive confirmation that the certificate was accepted.
			if _, err = stream.Recv(); status.Code(err) != codes.Aborted {
				t.Fatal(err)
			}
		},
	},
	{
		desc: "RotateCRLMissingType",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err)
			}

			err = stream.Send(&certz.RotateCertificateRequest{
				RotateRequest: &certz.RotateCertificateRequest_Certificates{
					Certificates: &certz.UploadRequest{
						Entities: []*certz.Entity{
							{
								Version:   "test",
								CreatedOn: 123,
								Entity: &certz.Entity_CertificateRevocationListBundle{
									CertificateRevocationListBundle: &certz.CertificateRevocationListBundle{
										CertificateRevocationLists: []*certz.CertificateRevocationList{
											&certz.CertificateRevocationList{
												Encoding:                  certz.CertificateEncoding_CERTIFICATE_ENCODING_PEM,
												CertificateRevocationList: []byte(`test`),
											},
										},
									},
								},
							},
						},
					},
				},
			})
			if err != nil {
				t.Fatal(err)
			}

			// 2) Receive confirmation that the certificate was accepted.
			if _, err = stream.Recv(); status.Code(err) != codes.Aborted {
				t.Fatal(err)
			}
		},
	},
	{
		desc: "RotateCRLMissingEncoding",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err)
			}

			err = stream.Send(&certz.RotateCertificateRequest{
				RotateRequest: &certz.RotateCertificateRequest_Certificates{
					Certificates: &certz.UploadRequest{
						Entities: []*certz.Entity{
							{
								Version:   "test",
								CreatedOn: 123,
								Entity: &certz.Entity_CertificateRevocationListBundle{
									CertificateRevocationListBundle: &certz.CertificateRevocationListBundle{
										CertificateRevocationLists: []*certz.CertificateRevocationList{
											&certz.CertificateRevocationList{
												Type:                      certz.CertificateType_CERTIFICATE_TYPE_X509,
												CertificateRevocationList: []byte(`test`),
											},
										},
									},
								},
							},
						},
					},
				},
			})
			if err != nil {
				t.Fatal(err)
			}

			// 2) Receive confirmation that the certificate was accepted.
			if _, err = stream.Recv(); status.Code(err) != codes.Aborted {
				t.Fatal(err)
			}
		},
	},
	{
		desc: "AddProfileUnimplemented",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			if _, err := sc.AddProfile(ctx, &certz.AddProfileRequest{}, grpc.EmptyCallOption{}); err != nil && status.Code(err) != codes.Unimplemented {
				t.Error("Expected Unimplemented Error")
			}
		},
	},
	{
		desc: "DeleteProfileUnimplemented",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			if _, err := sc.DeleteProfile(ctx, &certz.DeleteProfileRequest{}, grpc.EmptyCallOption{}); err != nil && status.Code(err) != codes.Unimplemented {
				t.Error("Expected Unimplemented Error")
			}
		},
	},
	{
		desc: "GetProfileListUnimplemented",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			if _, err := sc.GetProfileList(ctx, &certz.GetProfileListRequest{}, grpc.EmptyCallOption{}); err != nil && status.Code(err) != codes.Unimplemented {
				t.Error("Expected Unimplemented Error")
			}
		},
	},
	{
		desc: "CanGenerateCSRAccept",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			if resp, _ := sc.CanGenerateCSR(ctx, &certz.CanGenerateCSRRequest{Params: &certz.CSRParams{CommonName: "test"}}, grpc.EmptyCallOption{}); resp.GetCanGenerate() != true {
				t.Errorf("CanGenerateCSR want: true got: %#v", resp)
			}
		},
	},
	{
		desc: "CanGenerateCSRReject",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			if resp, _ := sc.CanGenerateCSR(ctx, &certz.CanGenerateCSRRequest{}, grpc.EmptyCallOption{}); resp.GetCanGenerate() != false {
				t.Errorf("CanGenerateCSR want: false got: %#v", resp)
			}
		},
	},
	{
		desc: "GenerateCsrRSA",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err)
			}
			err = stream.Send(&certz.RotateCertificateRequest{
				RotateRequest: &certz.RotateCertificateRequest_GenerateCsr{
					GenerateCsr: &certz.GenerateCSRRequest{
						Params: &certz.CSRParams{
							CsrSuite:   certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_RSA_2048_SIGNATURE_ALGORITHM_SHA_2_256,
							CommonName: "test",
							// Country: "US",
							// State: "CA",
							// City: "Sunnyvale",
							// Organization: "Google",
							// OrganizationalUnit: "Test",
						},
					},
				},
			},
			)
			if err != nil {
				t.Fatal(err)
			}

			_, err = stream.Recv()
			if err != nil {
				t.Fatal(err)
			}

			stream.CloseSend()
			_, err = stream.Recv()
			// No finalize results in an Aborted
			if err != nil && status.Code(err) != codes.Aborted {
				t.Fatalf("Unexpected error: %v", err)
			}
		},
	},
	{
		desc: "GenerateCsrECDSA",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err)
			}

			err = stream.Send(&certz.RotateCertificateRequest{
				RotateRequest: &certz.RotateCertificateRequest_GenerateCsr{
					GenerateCsr: &certz.GenerateCSRRequest{
						Params: &certz.CSRParams{
							CsrSuite:           certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_ECDSA_PRIME256V1_SIGNATURE_ALGORITHM_SHA_2_256,
							CommonName:         "test",
							Country:            "US",
							State:              "CA",
							City:               "Sunnyvale",
							Organization:       "Google",
							OrganizationalUnit: "Test",
						},
					},
				},
			},
			)
			if err != nil {
				t.Fatal(err)
			}

			_, err = stream.Recv()
			if err != nil {
				t.Fatal(err)
			}

			stream.CloseSend()
			_, err = stream.Recv()
			// No finalize results in an Aborted
			if err != nil && status.Code(err) != codes.Aborted {
				t.Fatalf("Unexpected error: %v", err)
			}
		},
	},
	{
		desc: "GenerateCsrAttest",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err)
			}

			err = stream.Send(&certz.RotateCertificateRequest{
				RotateRequest: &certz.RotateCertificateRequest_GenerateCsr{
					GenerateCsr: &certz.GenerateCSRRequest{
						Params: &certz.CSRParams{
							CsrSuite:           certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_ECDSA_PRIME256V1_SIGNATURE_ALGORITHM_SHA_2_256,
							CommonName:         "test",
							Country:            "US",
							State:              "CA",
							City:               "Sunnyvale",
							Organization:       "Google",
							OrganizationalUnit: "Test",
						},
					},
				},
			},
			)
			if err != nil {
				t.Fatal(err)
			}

			_, err = stream.Recv()
			if err != nil {
				t.Fatal(err)
			}

			stream.CloseSend()
			_, err = stream.Recv()
			// No finalize results in an Aborted
			if err != nil && status.Code(err) != codes.Aborted {
				t.Fatalf("Unexpected error: %v", err)
			}
		},
	},
}

/*
func TestGnsiCertzServer(t *testing.T) {
	if err := os.MkdirAll(filepath.Join(crlConfigTestPath, "crl"), 0777); err != nil {
		t.Fatalf("Failed Creating CRL Default dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(crlConfigTestPath, "crl_flush"), 0777); err != nil {
		t.Fatalf("Failed Creating CRL Default dir: %v", err)
	}
	s := createMTLSServer(t, CACertV1)
	if s == nil {
		t.Fatal("Creating mTLS server failed.")
	}
	port := s.config.Port
	go runServer(t, s)
	defer func() {
		s.Cleanup()
		muPath.Lock()
		defer muPath.Unlock()
		if _, err := rmSymlink(CACert); err != nil {
			t.Errorf("Cannot delete CA cert symlink: %v", err)
		}
		if _, err := rmSymlink(srvTestCertLink); err != nil {
			t.Errorf("Cannot delete server cert symlink: %v", err)
		}
		if _, err := rmSymlink(srvTestKeyLink); err != nil {
			t.Errorf("Cannot delete server key symlink: %v", err)
		}
		if err := deleteCredentialFiles(s.config); err != nil {
			t.Errorf("Cannot delete credentials: %v", err)
		}
		if err := cleanupCRLfiles(); err != nil {
			t.Errorf("Failed to cleanup crl files: %v", err)
		}
	}()

	if err := resetSrvCertKeyToV1(s.config); err != nil {
		t.Fatalf("couldn't reset server certificate/key: %v", err)
	}
	var mu sync.Mutex
	for _, test := range gnsiCertzTestCases {
		ctx, cancel := context.WithCancel(context.Background())

		caCertPem, err := os.ReadFile(CACert)
		if err != nil {
			cancel()
			t.Fatalf("could not read CA certificate: %s", err)
		}
		rootCA := x509.NewCertPool()
		if ok := rootCA.AppendCertsFromPEM(caCertPem); !ok {
			cancel()
			t.Fatalf("could not load CA certificate")
		}
		certificate, err := tls.LoadX509KeyPair(CCert, CKey)
		if err != nil {
			cancel()
			t.Fatalf("could not load client key pair: %s", err)
		}
		tlsConfig := &tls.Config{
			RootCAs:      rootCA,
			Certificates: []tls.Certificate{certificate},
			ServerName:   "mydomain.com",
		}
		opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}
		targetAddr := fmt.Sprintf("127.0.0.1:%d", port)

		t.Run(test.desc, func(t *testing.T) {
			mu.Lock()
			defer mu.Unlock()
			conn, err := grpc.Dial(targetAddr, opts...)
			if err != nil {
				cancel()
				t.Fatalf("Dialing to %s failed: %v", targetAddr, err)
			}
			defer conn.Close()

			sc := certz.NewCertzClient(conn)
			test.f(ctx, t, sc, s)
		})
		cancel()
	}
}
*/
/*
		// Test RPCs during NSF freeze mode.
		s.WarmRestartHelper.SetFreezeStatus(true)
		t.Run("RotateUnavailableDuringFreeze", func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())

			caCertPem, err := os.ReadFile(CACert)
			if err != nil {
				cancel()
				t.Fatalf("could not read CA certificate: %s", err)
			}
			rootCA := x509.NewCertPool()
			if ok := rootCA.AppendCertsFromPEM(caCertPem); !ok {
				cancel()
				t.Fatalf("could not load CA certificate")
			}
			certificate, err := tls.LoadX509KeyPair(CCert, CKey)
			if err != nil {
				cancel()
				t.Fatalf("could not load client key pair: %s", err)
			}
			tlsConfig := &tls.Config{
				RootCAs:      rootCA,
				Certificates: []tls.Certificate{certificate},
				ServerName:   "mydomain.com",
			}
			opts := []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig))}
			targetAddr := fmt.Sprintf("127.0.0.1:%d", port)

			conn, err := grpc.Dial(targetAddr, opts...)
			if err != nil {
				cancel()
				t.Fatalf("Dialing to %s failed: %v", targetAddr, err)
			}
			defer conn.Close()

			sc := certz.NewCertzClient(conn)
			stream, err := sc.Rotate(ctx, grpc.EmptyCallOption{})
			if err != nil {
				t.Fatal(err)
			}
			if err = stream.Send(&certz.RotateCertificateRequest{}); err != nil {
				t.Fatal(err)
			}
			_, err = stream.Recv()
			testErr(err, codes.Unavailable, "RPC disabled since NSF is ongoing!", t)
		})
		s.WarmRestartHelper.SetFreezeStatus(false)
	}
*/
func deleteCredentialFiles(cfg *Config) error {
	for _, f := range []string{SCertV2, SKeyV2, SCertV1, SKeyV1, CACertV1, srvTestKeyLink, srvTestCertLink} {
		if err := os.Remove(f); err != nil {
			log.V(lvl.DEBUG).Infoln(err)
		}
	}
	return nil
}

func cleanupCRLfiles() error {
	if err := os.RemoveAll(filepath.Join(crlConfigTestPath, "crl")); err != nil {
		return err
	}
	return nil
}

// copyCred copies data from golden copy of a credentials file into a new one.
func copyCred(src, dst string) error {
	txt, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("Could not load test credential file: %v", err)
	}
	srcinfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, txt, srcinfo.Mode())
}

func resetGRPCCertMetadataFile(path string) error {
	buf := []byte(`{"gnxi":{"profile_id":"gnxi","active":{"certificate":{"EType":0,"CreatedOn":1,"Version":"startCert","CertPath":"/testdata/mtls/server_V1_cert.pem","KeyPath":"/testdata/mtls/server_V1_key.pem","Final":true},"trust_bundle":{"EType":1,"CreatedOn":2,"Version":"caStart","CertPath":"/testdata/mtls/ca_V1_bundle.pem","KeyPath":"","Final":true},"crl_bundle":{"EType":2,"CreatedOn":3,"Version":"crlStart","CertPath":"../testdata/mtls","KeyPath":"","Final":true},"auth_policy":{"EType":3,"CreatedOn":4,"Version":"apStart","CertPath":"unknown","KeyPath":"","Final":true}},"last_active":{"certificate":{"EType":0,"CreatedOn":0,"Version":"1","CertPath":"","KeyPath":"","Final":false},"trust_bundle":{"EType":0,"CreatedOn":0,"Version":"2","CertPath":"","KeyPath":"","Final":false},"crl_bundle":{"EType":0,"CreatedOn":0,"Version":"3","CertPath":"","KeyPath":"","Final":false},"auth_policy":{"EType":0,"CreatedOn":0,"Version":"4","CertPath":"","KeyPath":"","Final":false}}}}`)
	return attemptWrite(path, buf, 0644)
}

func resetSrvCertKeyToV1(cfg *Config) error {
	muPath.Lock()
	defer muPath.Unlock()
	// Create credential files in testdata directory.
	for _, f := range []struct{ gold, dst string }{{GoldCACertV1, CACertV1}, {GoldSCertV1, SCertV1}, {GoldSKeyV1, SKeyV1}} {
		if err := copyCred(f.gold, f.dst); err != nil {
			return fmt.Errorf("Failed to copy %s to %s: %w", f.gold, f.dst, err)
		}
	}
	if err := resetGRPCCertMetadataFile(cfg.CertzMetaFile); err != nil {
		return err
	}
	return atomicSetSrvCertKeyPair(cfg, SCertV1, SKeyV1)
}

func generateVersion() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func getLinkTarget(t *testing.T, lnk string) os.FileInfo {
	t.Helper()
	if _, err := os.Lstat(lnk); os.IsNotExist(err) {
		t.Errorf("link '%s' does not exist", lnk)
	}
	// The symbolic link exists. Get where it points to.
	trgt, err := os.Readlink(lnk)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Stat(trgt)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func isLinkCorrect(t *testing.T, lnk, kind, id, ver string) {
	t.Helper()
	f := getLinkTarget(t, lnk)
	p := regexp.MustCompile(fmt.Sprintf("^%v_%v_[0-9]+_%v.pem", id, ver, kind))
	err := fmt.Errorf("link '%s' does not point to '%s_%s_*_%s.pem'. It points to '%s'", lnk, id, ver, kind, f.Name())
	if !p.MatchString(f.Name()) {
		t.Fatal(err)
	}
}
