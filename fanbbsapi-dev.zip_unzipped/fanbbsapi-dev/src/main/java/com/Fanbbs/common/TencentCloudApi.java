package com.Fanbbs.common;

import javax.crypto.Mac;
import javax.crypto.spec.SecretKeySpec;
import java.nio.charset.StandardCharsets;
import java.security.InvalidKeyException;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;

public class TencentCloudApi {
    private static final String SECRET_ID = "AKIDLsT9fyOxdFkGykV9Z1RxRrCiO3nfYbdi";
    private static final String SECRET_KEY = "DWks7HHKC62nKl7geWTWXPA25HMLRes1";
    private static final String SERVICE = "sms";
    private static final String HOST = "sms.tencentcloudapi.com";
    private static final String ENDPOINT = "https://" + HOST;
    private static final String REGION = "ap-guangzhou";
    private static final String ACTION = "SendSms";
    private static final String VERSION = "2021-01-11";
    private static final String ALGORITHM = "TC3-HMAC-SHA256";

//    public static void main(String[] args) throws NoSuchAlgorithmException, InvalidKeyException, IOException {
//        long timestamp = Instant.now().getEpochSecond(); // 获取当前时间戳
//        String date = DateTimeFormatter.ofPattern("yyyy-MM-dd")
//                .withZone(ZoneOffset.UTC)
//                .format(Instant.ofEpochSecond(timestamp)); // 格式化日期
//
//        String payload = "{\"PhoneNumberSet\":[\"+8618972868872\"],\"SmsSdkAppId\":\"1400856029\",\"SignName\":\"湖北纯萌科技\",\"TemplateId\":\"1895371\",\"TemplateParamSet\":[\"1234\"],\"SessionContext\":\"test\"}";
//
//        String authorization = generateAuthorization(payload, timestamp, date);
//
//        // 发送HTTP请求
//        URL url = new URL(ENDPOINT);
//        HttpURLConnection connection = (HttpURLConnection) url.openConnection();
//        connection.setRequestMethod("POST");
//        connection.setRequestProperty("Content-Type", "application/json; charset=utf-8");
//        connection.setRequestProperty("Authorization", authorization);
//        connection.setRequestProperty("Host", HOST);
//        connection.setRequestProperty("X-TC-Action", ACTION);
//        connection.setRequestProperty("X-TC-Version", VERSION);
//        connection.setRequestProperty("X-TC-Timestamp", String.valueOf(timestamp));
//        connection.setRequestProperty("X-TC-Region", REGION);
//        connection.setDoOutput(true);
//
//        // 发送请求体
//        try (OutputStream os = connection.getOutputStream()) {
//            byte[] input = payload.getBytes(StandardCharsets.UTF_8);
//            os.write(input, 0, input.length);
//        }
//
//        // 读取响应
//        int responseCode = connection.getResponseCode();
//        BufferedReader in = new BufferedReader(new InputStreamReader(connection.getInputStream(), StandardCharsets.UTF_8));
//        String inputLine;
//        StringBuilder response = new StringBuilder();
//        while ((inputLine = in.readLine()) != null) {
//            response.append(inputLine);
//        }
//        in.close();
//
//        // 打印响应
//        System.out.println("Response Code: " + responseCode);
//        System.out.println(response);
//    }

    private static String generateAuthorization(String payload, long timestamp, String date) throws NoSuchAlgorithmException, InvalidKeyException {
        String canonicalRequest = buildCanonicalRequest(payload);
        String stringToSign = buildStringToSign(canonicalRequest, timestamp, date);
        String signature = calculateSignature(stringToSign, date);
        String credentialScope = date + "/" + SERVICE + "/tc3_request";
        String signedHeaders = "content-type;host;x-tc-action";

        return ALGORITHM + " " +
                "Credential=" + SECRET_ID + "/" + credentialScope + ", " +
                "SignedHeaders=" + signedHeaders + ", " +
                "Signature=" + signature;
    }

    private static String buildCanonicalRequest(String payload) {
        String httpRequestMethod = "POST";
        String canonicalUri = "/";
        String canonicalQueryString = "";
        String contentType = "application/json; charset=utf-8";
        String canonicalHeaders = "content-type:" + contentType + "\nhost:" + HOST + "\nx-tc-action:" + ACTION.toLowerCase() + "\n";
        String signedHeaders = "content-type;host;x-tc-action";
        String hashedRequestPayload;
        try {
            hashedRequestPayload = sha256Hex(payload);
        } catch (NoSuchAlgorithmException e) {
            throw new RuntimeException(e);
        }
        return httpRequestMethod + "\n" +
                canonicalUri + "\n" +
                canonicalQueryString + "\n" +
                canonicalHeaders + "\n" +
                signedHeaders + "\n" +
                hashedRequestPayload;
    }

    private static String buildStringToSign(String canonicalRequest, long timestamp, String date) {
        String credentialScope = date + "/" + SERVICE + "/tc3_request";
        String hashedCanonicalRequest;
        try {
            hashedCanonicalRequest = sha256Hex(canonicalRequest);
        } catch (NoSuchAlgorithmException e) {
            throw new RuntimeException(e);
        }
        return ALGORITHM + "\n" +
                timestamp + "\n" +
                credentialScope + "\n" +
                hashedCanonicalRequest;
    }

    private static String calculateSignature(String stringToSign, String date) throws NoSuchAlgorithmException, InvalidKeyException {
        byte[] secretDate = hmacSha256(("TC3" + SECRET_KEY).getBytes(StandardCharsets.UTF_8), date);
        byte[] secretService = hmacSha256(secretDate, SERVICE);
        byte[] secretSigning = hmacSha256(secretService, "tc3_request");
        return bytesToHex(hmacSha256(secretSigning, stringToSign));
    }

    private static byte[] hmacSha256(byte[] key, String data) throws NoSuchAlgorithmException, InvalidKeyException {
        Mac mac = Mac.getInstance("HmacSHA256");
        mac.init(new SecretKeySpec(key, "HmacSHA256"));
        return mac.doFinal(data.getBytes(StandardCharsets.UTF_8));
    }

    private static String sha256Hex(String data) throws NoSuchAlgorithmException {
        return bytesToHex(MessageDigest.getInstance("SHA-256").digest(data.getBytes(StandardCharsets.UTF_8)));
    }

    private static String bytesToHex(byte[] bytes) {
        StringBuilder sb = new StringBuilder();
        for (byte b : bytes) {
            sb.append(String.format("%02x", b));
        }
        return sb.toString();
    }
}
