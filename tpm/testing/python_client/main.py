import requests

response = requests.get('https://server.domain.com:18081/', verify='../../certs/tls-root-ca.crt', cert=('../../certs/cert.pem', '../../certs/tpmkey.pem'))

print("Status Code: %s" % response.status_code)
print(response.text)