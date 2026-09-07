/*
# export OPENSSL_MODULES=/usr/lib/x86_64-linux-gnu/ossl-modules/
# 
# cat /etc/ssl/openssl.cnf
# [openssl_init]
# providers = provider_sect
# ssl_conf = ssl_sect

# [provider_sect]
# default = default_sect
# tpm2 = tpm2_sect

# [tpm2_sect]
# activate = 1
#
# [default_sect]
# activate = 1


$ openssl list --providers
    Providers:
    default
        name: OpenSSL Default Provider
        version: 3.0.2
        status: active
    tpm2
        name: TPM 2.0 Provider
        version: 1.3.0
        status: active

gcc main.c -lcrypto -lssl -o client



note some of the following code is written by gpu's
*/


#include <stdio.h>
#include <unistd.h>
#include <string.h>
#include <signal.h>
#include <sys/socket.h>
#include <arpa/inet.h>
#include <openssl/ssl.h>
#include <openssl/err.h>
#include <openssl/provider.h>
#include <stdlib.h>
#include <arpa/inet.h>
#include <netdb.h>



#define HOST "server.domain.com"
#define PORT 18081
#define BUFFER_SIZE 4096

void init_openssl() {
    SSL_load_error_strings();
    OpenSSL_add_ssl_algorithms();
}

void cleanup_openssl() {
    EVP_cleanup();
}

SSL_CTX *create_context() {
    const SSL_METHOD *method;
    SSL_CTX *ctx;

    method = TLS_client_method(); // Supports negotiation up to TLS 1.3
    ctx = SSL_CTX_new(method);
    if (!ctx) {
        perror("Unable to create SSL context");
        ERR_print_errors_fp(stderr);
        exit(EXIT_FAILURE);
    }
    return ctx;
}

void configure_context(SSL_CTX *ctx, const char *ca_file, const char *cert_file, const char *key_file) {
    // 1. Load Root CA to verify the server's certificate
    if (SSL_CTX_load_verify_locations(ctx, ca_file, NULL) <= 0) {
        ERR_print_errors_fp(stderr);
        exit(EXIT_FAILURE);
    }

    // Require server certificate verification
    SSL_CTX_set_verify(ctx, SSL_VERIFY_PEER, NULL);

    // 2. Load the Client Certificate (for mTLS)
    if (SSL_CTX_use_certificate_file(ctx, cert_file, SSL_FILETYPE_PEM) <= 0) {
        ERR_print_errors_fp(stderr);
        exit(EXIT_FAILURE);
    }

    // 3. Load the Client Private Key (for mTLS)
    if (SSL_CTX_use_PrivateKey_file(ctx, key_file, SSL_FILETYPE_PEM) <= 0) {
        ERR_print_errors_fp(stderr);
        exit(EXIT_FAILURE);
    }

    // Verify if the private key matches the client certificate
    if (!SSL_CTX_check_private_key(ctx)) {
        fprintf(stderr, "Private key does not match the public certificate\n");
        exit(EXIT_FAILURE);
    }
}

int create_socket(const char *hostname, int port) {
    struct hostent *host;
    struct sockaddr_in addr;
    int s;

    if ((host = gethostbyname(hostname)) == NULL) {
        perror("Host resolution failed");
        exit(EXIT_FAILURE);
    }

    s = socket(AF_INET, SOCK_STREAM, 0);
    if (s < 0) {
        perror("Unable to create socket");
        exit(EXIT_FAILURE);
    }

    memset(&addr, 0, sizeof(addr));
    addr.sin_family = AF_INET;
    addr.sin_port = htons(port);
    addr.sin_addr = *((struct in_addr *)host->h_addr_list[0]);

    if (connect(s, (struct sockaddr*)&addr, sizeof(addr)) < 0) {
        perror("Unable to connect to server");
        close(s);
        exit(EXIT_FAILURE);
    }

    return s;
}


int main(int argc, char **argv)
{


    OSSL_PROVIDER* provider;

    provider = OSSL_PROVIDER_load(NULL, "default");
    if (provider == NULL) {
        printf("Failed to load Default provider\n");
        exit(EXIT_FAILURE);
    }
    printf("Default Provider name: %s\n", OSSL_PROVIDER_get0_name(provider));

    OSSL_PROVIDER* custom_provider = OSSL_PROVIDER_load(NULL, "tpm2");
    if (custom_provider == NULL) {
      perror("Could not create custom provider");
      exit(EXIT_FAILURE);
    }
    printf("Custom Provider name: %s\n", OSSL_PROVIDER_get0_name(custom_provider));


    init_openssl();
    SSL_CTX *ctx = create_context();

    // Pass paths to CA cert, Client cert, and Client private key
    configure_context(ctx, "../certs/tls-root-ca.crt", "../certs/cert.pem", "../certs/tpmkey.pem");

    // Establish raw TCP connection
    int server_fd = create_socket(HOST, PORT);

    // Bind SSL layer to the file descriptor
    SSL *ssl = SSL_new(ctx);
    SSL_set_fd(ssl, server_fd);

    // Perform TLS Handshake (mTLS negotiation happens here)
    if (SSL_connect(ssl) <= 0) {
        ERR_print_errors_fp(stderr);
    } else {
        printf("Connected via %s encryption\n", SSL_get_cipher(ssl));

        // Format a basic HTTPS GET request
        char request[BUFFER_SIZE];
        snprintf(request, sizeof(request),
                 "GET /index.html HTTP/1.0\r\n"
                 "Host: %s\r\n"
                 "User-Agent: OpenSSL-C-Client\r\n"
                 "Connection: close\r\n\r\n", HOST);

        // Send HTTP Request
        SSL_write(ssl, request, strlen(request));

        // Read and display HTTP Response
        char response[BUFFER_SIZE];
        int bytes;
        while ((bytes = SSL_read(ssl, response, sizeof(response) - 1)) > 0) {
            response[bytes] = '\0';
            printf("%s", response);
        }
    }

    // Cleanup
    SSL_shutdown(ssl);
    SSL_free(ssl);
    close(server_fd);
    SSL_CTX_free(ctx);
    cleanup_openssl();

    return 0;
}
