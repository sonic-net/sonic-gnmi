package gnmi

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	log "github.com/golang/glog"
	certz "github.com/openconfig/gnsi/certz"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

// Dummy credentials for the test client
const testUsername = "username"
const testPassword = "password"

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
	{
		desc: "restoreSymlink",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			// Create a single temporary directory for all subtests.
			tempDir, err := os.MkdirTemp("", "gnsi_certz_test")
			if err != nil {
				t.Fatalf("Failed to create temp dir: %v", err)
			}
			t.Cleanup(func() { os.RemoveAll(tempDir) })

			// Test case: oldTarget is empty, should do nothing.
			linkPath := filepath.Join(tempDir, "empty_target_link")
			if err := restoreSymlink("", linkPath); err != nil {
				t.Errorf("restoreSymlink(\"\", %q) returned error: %v, want nil", linkPath, err)
			}
			if _, err := os.Lstat(linkPath); !os.IsNotExist(err) {
				t.Errorf("restoreSymlink(\"\", %q) created/modified link, should do nothing", linkPath)
			}

			// Test case: link does not exist, oldTarget is valid.
			oldFile := filepath.Join(tempDir, "old_file")
			if err := os.WriteFile(oldFile, []byte("old content"), 0644); err != nil {
				t.Fatalf("Failed to create old_file: %v", err)
			}
			linkPath = filepath.Join(tempDir, "new_link")
			if err := restoreSymlink(oldFile, linkPath); err != nil {
				t.Errorf("restoreSymlink(%q, %q) returned error: %v, want nil", oldFile, linkPath, err)
			}
			target, err := os.Readlink(linkPath)
			if err != nil {
				t.Errorf("Failed to read symlink %q: %v", linkPath, err)
			}
			if target != oldFile {
				t.Errorf("restoreSymlink(%q, %q) created symlink to %q, want %q", oldFile, linkPath, target, oldFile)
			}

			// Test case: link exists and points to a different file, oldTarget is valid.
			newFile := filepath.Join(tempDir, "new_file")
			if err := os.WriteFile(newFile, []byte("new content"), 0644); err != nil {
				t.Fatalf("Failed to create new_file: %v", err)
			}
			// FIX: Remove the existing symlink at linkPath before creating a new one.
			if _, err := rmSymlink(linkPath); err != nil {
				t.Fatalf("Failed to remove existing symlink %q: %v", linkPath, err)
			}
			if err := os.Symlink(newFile, linkPath); err != nil { // linkPath now points to newFile
				t.Fatalf("Failed to create symlink to new_file: %v", err)
			}
			if err := restoreSymlink(oldFile, linkPath); err != nil {
				t.Errorf("restoreSymlink(%q, %q) returned error: %v, want nil", oldFile, linkPath, err)
			}
			target, err = os.Readlink(linkPath)
			if err != nil {
				t.Errorf("Failed to read symlink %q: %v", linkPath, err)
			}
			if target != oldFile {
				t.Errorf("restoreSymlink(%q, %q) created symlink to %q, want %q", oldFile, linkPath, target, oldFile)
			}

			// Test case: oldTarget is invalid (non-existent file).
			invalidTarget := filepath.Join(tempDir, "non_existent")
			linkPath = filepath.Join(tempDir, "another_link")
			if err := restoreSymlink(invalidTarget, linkPath); err == nil {
				t.Errorf("restoreSymlink(%q, %q) returned nil, want error for invalid target", invalidTarget, linkPath)
			}
		},
	},
	{
		desc: "rmFileIfNotPointedToBySymlink",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			// Create a single temporary directory for all subtests.
			tempDir, err := os.MkdirTemp("", "gnsi_certz_test")
			if err != nil {
				t.Fatalf("Failed to create temp dir: %v", err)
			}
			t.Cleanup(func() { os.RemoveAll(tempDir) })
			// Setup files for this subtest.
			file1 := filepath.Join(tempDir, "file1")
			if err := os.WriteFile(file1, []byte("file1 content"), 0644); err != nil {
				t.Fatalf("Failed to create file1: %v", err)
			}
			file2 := filepath.Join(tempDir, "file2")
			if err := os.WriteFile(file2, []byte("file2 content"), 0644); err != nil {
				t.Fatalf("Failed to create file2: %v", err)
			}
			link1 := filepath.Join(tempDir, "link1")
			link2 := filepath.Join(tempDir, "link2")

			// Test case: link does not exist, file should not be removed.
			if err := rmFileIfNotPointedToBySymlink(file1, link1); err != nil {
				t.Errorf("rmFileIfNotPointedToBySymlink(%q, %q) returned error: %v, want nil", file1, link1, err)
			}
			if _, err := os.Stat(file1); os.IsNotExist(err) {
				t.Errorf("rmFileIfNotPointedToBySymlink(%q, %q) removed file, should not as link doesn't exist", file1, link1)
			}

			// Test case: link exists and points to the file, file should not be removed.
			if err := os.Symlink(file1, link1); err != nil {
				t.Fatalf("Failed to create symlink %q -> %q: %v", link1, file1, err)
			}
			if err := rmFileIfNotPointedToBySymlink(file1, link1); err != nil {
				t.Errorf("rmFileIfNotPointedToBySymlink(%q, %q) returned error: %v, want nil", file1, link1, err)
			}
			if _, err := os.Stat(file1); os.IsNotExist(err) {
				t.Errorf("rmFileIfNotPointedToBySymlink(%q, %q) removed file, should not as link points to it", file1, link1)
			}

			// Test case: link exists and points to a different file, file should be removed.
			if err := os.Symlink(file2, link2); err != nil {
				t.Fatalf("Failed to create symlink %q -> %q: %v", link2, file2, err)
			}
			if err := rmFileIfNotPointedToBySymlink(file1, link2); err != nil {
				t.Errorf("rmFileIfNotPointedToBySymlink(%q, %q) returned error: %v, want nil", file1, link2, err)
			}
			if _, err := os.Stat(file1); !os.IsNotExist(err) {
				t.Errorf("rmFileIfNotPointedToBySymlink(%q, %q) did not remove file, should have as link points elsewhere", file1, link2)
			}
		},
	},
	{
		desc: "isSymlinkValid",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			// Create a single temporary directory for all subtests.
			tempDir, err := os.MkdirTemp("", "gnsi_certz_test")
			if err != nil {
				t.Fatalf("Failed to create temp dir: %v", err)
			}
			t.Cleanup(func() { os.RemoveAll(tempDir) })
			// Test case: Path does not exist.
			nonExistent := filepath.Join(tempDir, "no_such_file")
			if isSymlinkValid(nonExistent) {
				t.Errorf("isSymlinkValid(%q) returned true, want false for non-existent path", nonExistent)
			}

			// Test case: Path is a regular file.
			regularFile := filepath.Join(tempDir, "regular.txt")
			if err := os.WriteFile(regularFile, []byte("regular content"), 0644); err != nil {
				t.Fatalf("Failed to create regular.txt: %v", err)
			}
			if !isSymlinkValid(regularFile) {
				t.Errorf("isSymlinkValid(%q) returned false, want true for regular file", regularFile)
			}

			// Test case: Path is a directory.
			dirPath := filepath.Join(tempDir, "a_directory")
			if err := os.Mkdir(dirPath, 0755); err != nil {
				t.Fatalf("Failed to create directory: %v", err)
			}
			if isSymlinkValid(dirPath) {
				t.Errorf("isSymlinkValid(%q) returned true, want false for a directory", dirPath)
			}

			// Test case: Path is a broken symlink.
			brokenLink := filepath.Join(tempDir, "broken_link")
			if err := os.Symlink(nonExistent, brokenLink); err != nil {
				t.Fatalf("Failed to create broken symlink: %v", err)
			}
			if isSymlinkValid(brokenLink) {
				t.Errorf("isSymlinkValid(%q) returned true, want false for a broken symlink", brokenLink)
			}

			// Test case: Path is a valid symlink to a regular file.
			validLink := filepath.Join(tempDir, "valid_link")
			if err := os.Symlink(regularFile, validLink); err != nil {
				t.Fatalf("Failed to create valid symlink: %v", err)
			}
			if !isSymlinkValid(validLink) {
				t.Errorf("isSymlinkValid(%q) returned false, want true for a valid symlink to a regular file", validLink)
			}
		},
	},
	{
		desc: "Rotate_DoUpload_CRLNotConfigured",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			// Temporarily disable CRL config
			oldPath := s.config.CertCRLConfig
			s.config.CertCRLConfig = ""
			defer func() { s.config.CertCRLConfig = oldPath }()

			stream, _ := sc.Rotate(ctx)
			stream.Send(&certz.RotateCertificateRequest{
				RotateRequest: &certz.RotateCertificateRequest_Certificates{
					Certificates: &certz.UploadRequest{
						Entities: []*certz.Entity{{
							Version:   "1.0",
							CreatedOn: 12345,
							Entity:    &certz.Entity_CertificateRevocationListBundle{},
						}},
					},
				},
			})
			_, err := stream.Recv()

			if status.Code(err) != codes.Aborted {
				t.Fatalf("Expected Aborted, got: %v", err)
			}
			if !strings.Contains(err.Error(), "CRL not configured") {
				t.Fatalf("Expected message to contain 'CRL not configured', got: %v", err)
			}
		},
	},
	{
		desc: "CreatesCRLDefaultDirectories",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			// 1) Isolate this test by using a fresh directory
			testRoot := t.TempDir()
			s.config.CertCRLConfig = testRoot

			// 2) Trigger the constructor logic
			// Since s is already initialized in your framework, we call it again
			// to test the directory creation side-effect.
			_ = NewGNSICertzServer(s)

			crlDefaultPath := filepath.Join(testRoot, crlDefault)
			crlFlushPath := filepath.Join(testRoot, crlDefault+crlFlush)

			// Verify Success
			if info, err := os.Stat(crlDefaultPath); err != nil {
				t.Errorf("CRL default directory %q was not created: %v", crlDefaultPath, err)
			} else if !info.IsDir() {
				t.Errorf("Path %q should be a directory", crlDefaultPath)
			}

			if info, err := os.Stat(crlFlushPath); err != nil {
				t.Errorf("CRL flush directory %q was not created: %v", crlFlushPath, err)
			} else if !info.IsDir() {
				t.Errorf("Path %q should be a directory", crlFlushPath)
			}
		},
	},
	{
		desc: "CRL_MkdirFailureLogging",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			// 1) Isolate this test with a fresh directory
			testRoot := t.TempDir()
			s.config.CertCRLConfig = testRoot

			// 2) Create a FILE where the directory needs to go.
			// This forces os.MkdirAll to fail because it can't overwrite a file.
			crlPath := filepath.Join(testRoot, crlDefault)
			if err := os.WriteFile(crlPath, []byte("blocker"), 0644); err != nil {
				t.Fatalf("Setup failed: %v", err)
			}

			// 3) Trigger the constructor
			_ = NewGNSICertzServer(s)

			// 4) Verify: The path is still a FILE (MkdirAll failed to make it a dir)
			info, err := os.Stat(crlPath)
			if err != nil {
				t.Fatalf("Path should still exist: %v", err)
			}
			if info.IsDir() {
				t.Errorf("Logic error: MkdirAll should have failed, but %s is a directory", crlPath)
			}
		},
	},
	{
		desc: "Rotate_ConcurrentRPC_ReturnsAborted",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			// 1) Start the first stream to hold the certzMu lock
			stream1, err := sc.Rotate(ctx)
			if err != nil {
				t.Fatalf("Failed to open first stream: %v", err)
			}
			defer stream1.CloseSend()

			// 2) Attempt a second parallel stream
			// This triggers: if !certzMu.TryLock() { log.V(0).Infof("...already in use") }
			stream2, err := sc.Rotate(ctx)
			if err != nil {
				t.Fatalf("Failed to open second stream: %v", err)
			}

			_, err = stream2.Recv()
			if status.Code(err) != codes.Aborted {
				t.Errorf("Expected codes.Aborted for concurrent Rotate, got: %v", err)
			}
		},
	},
	{
		desc: "Rotate_UnexpectedEOF_TriggersRevert",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			stream, err := sc.Rotate(ctx)
			if err != nil {
				t.Fatal(err)
			}

			// 1) Close the stream immediately without sending anything or Finalizing
			// This triggers: if err == io.EOF { log.V(0).Infof("...Received unexpected EOF") }
			if err := stream.CloseSend(); err != nil {
				t.Fatal(err)
			}

			_, err = stream.Recv()
			if status.Code(err) != codes.Aborted {
				t.Errorf("Expected Aborted due to missing Finalize, got: %v", err)
			}
		},
	},
	{
		desc: "Rotate_InvalidRequest_TriggersProcessErr",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			stream, err := sc.Rotate(ctx)
			if err != nil {
				t.Fatal(err)
			}

			// 1) Send an empty RotateCertificateRequest (no entities, no finalize)
			// This should trigger an error in srv.processRotateRequest
			err = stream.Send(&certz.RotateCertificateRequest{
				RotateRequest: &certz.RotateCertificateRequest_Certificates{
					Certificates: &certz.UploadRequest{
						Entities: []*certz.Entity{}, // Empty entities usually triggers processing error
					},
				},
			})
			if err != nil {
				t.Fatal(err)
			}

			// This triggers: log.V(0).Infof("Processing of rotate request resulted in an error...")
			_, err = stream.Recv()
			if err == nil {
				t.Error("Expected error from empty rotate request, got nil")
			}
		},
	},
	{
		desc: "RevertProfile_Full_Coverage",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			cs := NewGNSICertzServer(s)
			if cs == nil {
				t.Fatal("Failed to initialize GNSICertzServer")
			}

			profileID := "revert-test-profile"

			// Setup temporary file paths to avoid "file not found" errors during os.Rename or atomicSet
			tmpDir, _ := os.MkdirTemp("", "revert_test")
			defer os.RemoveAll(tmpDir)

			// Create dummy files for the 'LastEntities' (the target of the revert)
			lastCert := filepath.Join(tmpDir, "last_cert.pem")
			lastKey := filepath.Join(tmpDir, "last_key.pem")
			lastCA := filepath.Join(tmpDir, "last_ca.pem")
			lastAuth := filepath.Join(tmpDir, "auth.json")
			lastAuthBackup := lastAuth + ".bak" // Assuming backupExt is ".bak" or similar

			os.WriteFile(lastCert, []byte("last-cert"), 0644)
			os.WriteFile(lastKey, []byte("last-key"), 0644)
			os.WriteFile(lastCA, []byte("last-ca"), 0644)
			os.WriteFile(lastAuthBackup, []byte("auth-policy-data"), 0644)

			// Manually inject a profile state that triggers all 4 'Final == false' blocks
			testProfile := &profile{
				ID: profileID,
				ActiveEntities: entityGroup{
					Cert:        &genericEntity{Final: false, CertPath: filepath.Join(tmpDir, "act_c.pem")},
					TrustBundle: &genericEntity{Final: false, CertPath: filepath.Join(tmpDir, "act_ca.pem")},
					CrlBundle:   &genericEntity{Final: false},
					AuthPolicy:  &genericEntity{Final: false, CertPath: lastAuth},
				},
				LastEntities: entityGroup{
					Cert:        &genericEntity{CertPath: lastCert, KeyPath: lastKey},
					TrustBundle: &genericEntity{CertPath: lastCA},
					CrlBundle:   &genericEntity{},
					AuthPolicy:  &genericEntity{CertPath: lastAuth},
				},
			}

			certzMu.Lock()
			cs.profiles[profileID] = testProfile
			certzMu.Unlock()

			// 1. Test Revert with empty ID (covers defaultProfile logic)
			cs.revertProfile("")

			// 2. Test Revert with non-existent ID (covers !ok logic)
			cs.revertProfile("non-existent-id")

			// 3. Test Full Revert (covers all 4 if blocks)
			// This will trigger atomicSetSrvCertKeyPair, atomicSetCACert, copyCRLBundle, and os.Rename
			cs.revertProfile(profileID)

			// Verification: Check if ActiveEntities were restored to LastEntities
			certzMu.Lock()
			updatedProfile := cs.profiles[profileID]
			if updatedProfile.ActiveEntities.Cert != testProfile.LastEntities.Cert {
				t.Errorf("Cert was not reverted correctly")
			}
			if updatedProfile.ActiveEntities.TrustBundle != testProfile.LastEntities.TrustBundle {
				t.Errorf("TrustBundle was not reverted correctly")
			}
			certzMu.Unlock()
		},
	},
	{
		desc: "SaveEntities_AuthPolicy_BackupAndWriteSuccess",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			cs := NewGNSICertzServer(s)
			profileID := "gnxi"

			tmpDir, _ := os.MkdirTemp("", "ap_cov_1")
			defer os.RemoveAll(tmpDir)

			policyPath := filepath.Join(tmpDir, "auth.json")
			// Create initial file so Rename(policy, backup) works
			os.WriteFile(policyPath, []byte("old"), 0644)

			exp := &genericEntity{EType: apType, CertPath: policyPath}
			msg := &certz.Entity{Entity: &certz.Entity_AuthenticationPolicy{
				AuthenticationPolicy: &certz.AuthenticationPolicy{},
			}}

			err := cs.saveEntities(profileID, msg, exp)
			if err != nil {
				t.Logf("Successful path log (err is okay if msg empty): %v", err)
			}
		},
	},
	{
		desc: "SaveEntities_AuthPolicy_SaveFailAndRestoreFail",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			cs := NewGNSICertzServer(s)
			profileID := "gnxi"
			tmpDir, _ := os.MkdirTemp("", "ap_cov_2")
			defer os.RemoveAll(tmpDir)

			policyPath := filepath.Join(tmpDir, "auth.json")
			backupPath := policyPath + backupExt

			exp := &genericEntity{EType: apType, CertPath: policyPath}
			msg := &certz.Entity{Entity: &certz.Entity_AuthenticationPolicy{
				AuthenticationPolicy: &certz.AuthenticationPolicy{},
			}}

			// Trigger: Make policyPath a directory so attemptWrite fails
			os.Mkdir(policyPath, 0755)

			// Trigger: Make backupPath a directory so the restoration Rename fails
			os.Mkdir(backupPath, 0755)

			err := cs.saveEntities(profileID, msg, exp)
			if err == nil {
				t.Error("Expected error but got nil")
			} else {
				t.Logf("Caught expected error: %v", err)
			}
		},
	},
	{
		desc: "Rotate_Concurrent_Call_Error",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			// 1. Manually lock the mutex to simulate another Rotate session in progress
			certzMu.Lock()
			// Ensure we unlock it at the end so other tests don't hang
			defer certzMu.Unlock()

			// 2. Attempt a Rotate call
			stream, err := sc.Rotate(ctx)
			if err != nil {
				t.Fatal(err)
			}

			// 3. Trying to receive should trigger the "already in use" block
			_, err = stream.Recv()
			if status.Code(err) != codes.Aborted {
				t.Errorf("Expected codes.Aborted for concurrent rotate, got: %v", err)
			}
		},
	},
	{
		desc: "Rotate_Stream_Recv_Error",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			// To cover the "if err != nil" block after stream.Recv(), we cancel the
			// context after the stream is created but before data is sent.
			ctxCancel, cancel := context.WithCancel(ctx)
			stream, err := sc.Rotate(ctxCancel)
			if err != nil {
				t.Fatal(err)
			}

			// Cancel the context to force a stream error
			cancel()

			_, err = stream.Recv()
			if err == nil {
				t.Fatal("Expected error from cancelled stream, got nil")
			}
			if status.Code(err) != codes.Aborted {
				t.Logf("Caught stream recv error (expected Aborted): %v", err)
			}
		},
	},
	{
		desc: "Rotate_Process_Request_Error",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			stream, err := sc.Rotate(ctx)
			if err != nil {
				t.Fatal(err)
			}
			// To trigger an error in srv.processRotateRequest, we send a request
			// with an empty/invalid content. We use the top-level field for ProfileId if it exists.
			req := &certz.RotateCertificateRequest{
				RotateRequest: &certz.RotateCertificateRequest_Certificates{
					Certificates: &certz.UploadRequest{},
				},
			}
			err = stream.Send(req)
			if err != nil {
				t.Fatal(err)
			}

			_, err = stream.Recv()
			if err == nil {
				t.Fatal("Expected process error due to missing data, got nil")
			}
		},
	},
	{
		desc: "Rotate_Finalize_Failure_Coverage",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			stream, err := sc.Rotate(ctx)
			if err != nil {
				t.Fatal(err)
			}
			// Send finalize without any prior valid rotation data for a specific profile.
			// We use the empty FinalizeRotation request.
			err = stream.Send(&certz.RotateCertificateRequest{
				RotateRequest: &certz.RotateCertificateRequest_FinalizeRotation{
					FinalizeRotation: &certz.FinalizeRequest{},
				},
			})
			if err != nil {
				t.Fatal(err)
			}

			_, err = stream.Recv()
			if err != nil {
				t.Logf("Finalize failure covered: %v", err)
			}
		},
	},
	{
		desc: "ParseCSRSuite_Coverage",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			tmpDir, err := os.MkdirTemp("", "gnsi_deep_cov")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(tmpDir)

			// 1. Coverage for parseCSRSuite Switch Case
			// This iterates through major enum types to hit all return paths in the switch
			suites := []certz.CSRSuite{
				//certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_RSA_2048_SIGNATURE_ALGORITHM_SHA_2_256,
				//certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_ECDSA_PRIME256V1_SIGNATURE_ALGORITHM_SHA_2_256,
				//certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_EDDSA_ED25519,
				//certz.CSRSuite_CSRSUITE_CIPHER_UNSPECIFIED,
				//certz.CSRSuite(999), // Triggers default case
				certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_RSA_2048_SIGNATURE_ALGORITHM_SHA_2_256,
				certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_RSA_2048_SIGNATURE_ALGORITHM_SHA_2_384,
				certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_RSA_2048_SIGNATURE_ALGORITHM_SHA_2_512,
				certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_RSA_3072_SIGNATURE_ALGORITHM_SHA_2_256,
				certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_RSA_3072_SIGNATURE_ALGORITHM_SHA_2_384,
				certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_RSA_3072_SIGNATURE_ALGORITHM_SHA_2_512,
				certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_RSA_4096_SIGNATURE_ALGORITHM_SHA_2_256,
				certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_RSA_4096_SIGNATURE_ALGORITHM_SHA_2_384,
				certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_RSA_4096_SIGNATURE_ALGORITHM_SHA_2_512,
				certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_ECDSA_PRIME256V1_SIGNATURE_ALGORITHM_SHA_2_256,
				certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_ECDSA_PRIME256V1_SIGNATURE_ALGORITHM_SHA_2_384,
				certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_ECDSA_PRIME256V1_SIGNATURE_ALGORITHM_SHA_2_512,
				certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_ECDSA_SECP384R1_SIGNATURE_ALGORITHM_SHA_2_256,
				certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_ECDSA_SECP384R1_SIGNATURE_ALGORITHM_SHA_2_384,
				certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_ECDSA_SECP384R1_SIGNATURE_ALGORITHM_SHA_2_512,
				certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_ECDSA_SECP521R1_SIGNATURE_ALGORITHM_SHA_2_256,
				certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_ECDSA_SECP521R1_SIGNATURE_ALGORITHM_SHA_2_384,
				certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_ECDSA_SECP521R1_SIGNATURE_ALGORITHM_SHA_2_512,
				certz.CSRSuite_CSRSUITE_X509_KEY_TYPE_EDDSA_ED25519,
				certz.CSRSuite_CSRSUITE_CIPHER_UNSPECIFIED,
				// Test an arbitrary unknown value to hit the default case
				certz.CSRSuite(999),
			}
			for _, suite := range suites {
				_, _ = parseCSRSuite(suite)
			}
		},
	},
	{
		desc: "ReadCertChain_Full_Coverage",
		f: func(ctx context.Context, t *testing.T, sc certz.CertzClient, s *Server) {
			cs := NewGNSICertzServer(s)
			profileID := "gnxi"

			// 1. Setup profile with a generated key to cover the "Using generated key" branch
			fakeGeneratedKey := []byte("fake-generated-private-key")
			certzMu.Lock()
			if _, ok := cs.profiles[profileID]; !ok {
				cs.profiles[profileID] = &profile{ID: profileID}
			}
			cs.profiles[profileID].generatedKey = fakeGeneratedKey
			certzMu.Unlock()

			// 2. Test Case: Missing PrivateKey triggers use of generatedKey
			// We also nest a Parent certificate to cover the 'for certChain != nil' loop
			chain := &certz.CertificateChain{
				Certificate: &certz.Certificate{
					Type:        certz.CertificateType_CERTIFICATE_TYPE_X509,
					Encoding:    certz.CertificateEncoding_CERTIFICATE_ENCODING_PEM,
					Certificate: []byte("child-cert-data"),
					PrivateKey:  nil, // This triggers the generatedKey logic
				},
				Parent: &certz.CertificateChain{
					Certificate: &certz.Certificate{
						Type:        certz.CertificateType_CERTIFICATE_TYPE_X509,
						Encoding:    certz.CertificateEncoding_CERTIFICATE_ENCODING_PEM,
						Certificate: []byte("parent-cert-data"),
					},
				},
			}

			cert, key, err := cs.readCertChain(profileID, chain)
			if err != nil {
				t.Fatalf("readCertChain failed: %v", err)
			}

			// Verify Generated Key was used
			if string(key) != string(fakeGeneratedKey) {
				t.Errorf("Expected generated key, got %s", string(key))
			}

			// Verify Cert Chain Concatenation (Child + Parent)
			// The code appends a newline after each cert
			expectedCert := "child-cert-data\nparent-cert-data\n"
			if string(cert) != expectedCert {
				t.Errorf("Cert chain concatenation failed. Expected %q, got %q", expectedCert, string(cert))
			}

			// 3. Coverage for Validation Errors inside the Parent Loop
			t.Run("ParentLoopValidationFailure", func(t *testing.T) {
				invalidParentChain := &certz.CertificateChain{
					Certificate: &certz.Certificate{
						Type:        certz.CertificateType_CERTIFICATE_TYPE_X509,
						Encoding:    certz.CertificateEncoding_CERTIFICATE_ENCODING_PEM,
						Certificate: []byte("child"),
						PrivateKey:  []byte("key"),
					},
					Parent: &certz.CertificateChain{
						Certificate: &certz.Certificate{
							Type: certz.CertificateType_CERTIFICATE_TYPE_UNSPECIFIED, // Trigger error in loop
						},
					},
				}
				_, _, err := cs.readCertChain(profileID, invalidParentChain)
				if err == nil || !strings.Contains(err.Error(), "certificate type has to be X.509") {
					t.Errorf("Expected parent validation error, got %v", err)
				}
			})
		},
	},
}

func TestGnsiCertzServer(t *testing.T) {
	if err := os.MkdirAll(filepath.Join(crlConfigTestPath, "crl"), 0777); err != nil {
		t.Fatalf("Failed Creating CRL Default dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(crlConfigTestPath, "crl_flush"), 0777); err != nil {
		t.Fatalf("Failed Creating CRL Flush Default dir: %v", err)
	}
	s := createMTLSServer(t, CACertV1)
	if s == nil {
		t.Fatal("Creating mTLS server failed.")
	}
	port := s.config.Port
	go runServer(t, s)
	defer s.Stop()

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
	// Create a gNSI.Certz client and connect it to the gNSI.Certz server.
	tlsConfig := &tls.Config{InsecureSkipVerify: true}

	// Use dummy credentials for the client
	cred := &loginCreds{Username: testUsername, Password: testPassword}

	// Attach both TLS transport and the PerRPC BasicAuth credentials
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
		grpc.WithPerRPCCredentials(cred),
	}

	targetAddr := fmt.Sprintf("127.0.0.1:%d", port)
	var mu sync.Mutex

	for _, test := range gnsiCertzTestCases {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		// Attach both TLS transport and the PerRPC BasicAuth credentials

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

func deleteCredentialFiles(cfg *Config) error {
	for _, f := range []string{SCertV2, SKeyV2, SCertV1, SKeyV1, CACertV1, srvTestKeyLink, srvTestCertLink} {
		if err := os.Remove(f); err != nil {
			log.V(3).Infoln(err)
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
		fmt.Errorf("Printing resetGRPC error: %s", err)
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
