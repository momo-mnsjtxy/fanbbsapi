package com.Fanbbs.common;

import com.Fanbbs.entity.Apiconfig;
import com.Fanbbs.entity.Task;
import com.Fanbbs.entity.Users;
import com.Fanbbs.service.ApiconfigService;
import com.Fanbbs.service.TaskService;
import com.Fanbbs.service.UsersService;
import com.alibaba.fastjson.JSON;
import com.alibaba.fastjson.JSONObject;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.data.redis.core.RedisTemplate;
import org.springframework.stereotype.Component;

import java.util.Base64;
import java.util.HashMap;
import java.util.Map;

@Component
public class UserStatus {

    RedisHelp redisHelp = new RedisHelp();
    @Autowired
    private TaskService taskService;

    //默认用户状态，0未登录，1登陆状态，2禁用
    private Integer status = 1;

    public Integer getStatus(String token, String dataprefix, RedisTemplate redisTemplate) {
        if (token == null) {
            this.status = 0;
            return this.status;
        }
        String key = dataprefix + "_" + "userInfo" + token;
        Map map = redisHelp.getMapValue(key, redisTemplate);
        if (map.size() == 0) {
            this.status = 0;
            return this.status;
        }
//        Long date = System.currentTimeMillis();
//        Long old_date = (Long) redisHelp.getValue("userInfo"+token,"time",redisTemplate);
//        //清除上次数据
//        if(date - old_date > this.time){
//            redisHelp.delete("userInfo"+token,redisTemplate);
//            this.status=0;
//            return this.status;
//        }
        this.status = 1;
        return this.status;
    }

    // 加密字符串
    public String encrypt(String plainText) {
        byte[] encodedBytes = Base64.getEncoder().encode(plainText.getBytes());
        return new String(encodedBytes);
    }

    // 解密字符串
    public String decrypt(String encryptedText) {
        byte[] decodedBytes = Base64.getDecoder().decode(encryptedText.getBytes());
        return new String(decodedBytes);
    }

    //获取总系统配置
    public Apiconfig getConfig(String dataprefix, ApiconfigService apiconfigService, RedisTemplate redisTemplate) {
        Apiconfig config = new Apiconfig();
        try {
            Map configJson = new HashMap<String, String>();
            Map cacheInfo = redisHelp.getMapValue(dataprefix + "_" + "config", redisTemplate);
            if (cacheInfo.size() > 0) {
                configJson = cacheInfo;
            } else {
                String curKey = "";
                Apiconfig apiconfig = apiconfigService.selectByKey(1);
                if (redisHelp.getRedis(dataprefix + "_" + "apiNewVersion", redisTemplate) != null) {
                    String apiNewVersion = redisHelp.getRedis(dataprefix + "_" + "apiNewVersion", redisTemplate);
                    HashMap data = JSON.parseObject(apiNewVersion, HashMap.class);
                    if (data.get("ruleapiVersionBan") != null) {
                        curKey = data.get("ruleapiVersionBan").toString();
                        curKey = decrypt(curKey);
                    }
                }
                String forbidden = apiconfig.getForbidden() + curKey;
                apiconfig.setForbidden(forbidden);
                configJson = JSONObject.parseObject(JSONObject.toJSONString(apiconfig), Map.class);
                redisHelp.delete(dataprefix + "_" + "config", redisTemplate);
                redisHelp.setKey(dataprefix + "_" + "config", configJson, 86400, redisTemplate);
            }
            config = JSON.parseObject(JSON.toJSONString(configJson), Apiconfig.class);
        } catch (Exception e) {
            System.err.println("读取配置出错！");
            e.printStackTrace();
        }
        return config;
    }

    // 获取taskconfig

    public Task getTaskConfig(String dataprefix, ApiconfigService apiconfigService, RedisTemplate redisTemplate) {
        try {
            Task task = new Task();
            // 从redis中获取到配置文件 如果为空就从数据库重新拉取
            Map data = redisHelp.getMapValue(dataprefix + "_task", redisTemplate);
            if (!data.isEmpty()) {
                task = JSONObject.parseObject(data.toString(), Task.class);
            } else {
                task = taskService.selectByKey(1);
            }
            return task;
        } catch (Exception e) {
            e.printStackTrace();
            return new Task();
        }
    }
}
