package com.Fanbbs.common;

import com.Fanbbs.entity.Task;
import com.Fanbbs.entity.Users;
import com.Fanbbs.service.TaskService;
import com.Fanbbs.service.UsersService;
import com.alibaba.fastjson.JSONObject;

import org.springframework.beans.factory.annotation.Value;
import org.springframework.data.redis.core.RedisTemplate;


import java.util.Map;

public class TaskUtils {

    RedisHelp redisHelp = new RedisHelp();
    baseFull baseFull = new baseFull();


    @Value("${web.prefix}")
    private String dataprefix;


    public void reward(String type, Users user, RedisTemplate redisTemplate, UsersService usersService, TaskService taskService) {
        Task task = getTask(redisTemplate, taskService);
        System.out.println(task);
        if (task == null) {
            System.out.println("task为空");
            return;
        }
        // 根据类型给予不同的奖励
        switch (type) {
            case "publish":
                publish(user, task, redisTemplate, usersService);
                break;
            case "comment":
                comment(user, task, redisTemplate, usersService);
                break;
            case "like":
                like(user, task, redisTemplate, usersService);
                break;
            case "mark":
                mark(user, task, redisTemplate, usersService);
                break;
            case "sign":
                sign(user, task, redisTemplate, usersService);
                break;
            case "view":
                view(user, task, redisTemplate, usersService);
                break;
            default:
                break;
        }
    }


    private Task getTask(RedisTemplate redisTemplate, TaskService taskService) {
        Task task = JSONObject.parseObject(JSONObject.toJSONString(redisHelp.getMapValue(dataprefix + "_task", redisTemplate)), Task.class);
        if (task.getId() == null) {
            task = taskService.selectByKey(1);
            Map<String, Object> data = JSONObject.parseObject(JSONObject.toJSONString(task), Map.class);
            redisHelp.setKey(dataprefix + "_task", data, 86400, redisTemplate);
            return task;
        }
        return task;
    }

    private void publish(Users user, Task task, RedisTemplate redisTemplate, UsersService usersService) {
        // 拿到用户信息之后更新用户对应的信息
        user.setExperience(user.getExperience() + task.getPublish_exp());
        user.setAssets(user.getAssets() + task.getPublish_point());
        // 记录次数
        String redisKey = "publish_" + user.getUid();
        int taskCount = 0;
        Object value = redisHelp.getRedis(redisKey, redisTemplate);
        if (value != null) {
            taskCount = Integer.parseInt((String) value )+ 1;
        }
        int secondsUntilEndOfDay = baseFull.endTime();
        redisHelp.setRedis(redisKey, String.valueOf(taskCount), secondsUntilEndOfDay, redisTemplate);
        if (taskCount < task.getPublish_times()) {
            usersService.update(user);
        }
    }

    private void comment(Users user, Task task, RedisTemplate redisTemplate, UsersService usersService) {
        // 拿到用户信息之后更新用户对应的信息
        user.setExperience(user.getExperience() + task.getComment_exp());
        user.setAssets(user.getAssets() + task.getComment_point());
        // 记录次数
        String redisKey = "comment_" + user.getUid();
        int taskCount = 0;
        Object value = redisHelp.getRedis(redisKey, redisTemplate);
        if (value != null) {
            taskCount = Integer.parseInt((String) value )+ 1;
        }
        int secondsUntilEndOfDay = baseFull.endTime();
        redisHelp.setRedis(redisKey, String.valueOf(taskCount), secondsUntilEndOfDay, redisTemplate);
        if (taskCount < task.getComment_times()) {
            usersService.update(user);
        }
    }

    private void like(Users user, Task task, RedisTemplate redisTemplate, UsersService usersService) {
        // 拿到用户信息之后更新用户对应的信息
        user.setExperience(user.getExperience() + task.getLike_exp());
        user.setAssets(user.getAssets() + task.getLike_point());
        // 记录次数
        String redisKey = "like_" + user.getUid();
        int taskCount = 0;
        Object value = redisHelp.getRedis(redisKey, redisTemplate);
        if (value != null) {
            taskCount = Integer.parseInt((String) value )+ 1;
        }
        int secondsUntilEndOfDay = baseFull.endTime();
        redisHelp.setRedis(redisKey, String.valueOf(taskCount), secondsUntilEndOfDay, redisTemplate);
        if (taskCount < task.getLike_times()) {
            usersService.update(user);
        }
    }

    private void sign(Users user, Task task, RedisTemplate redisTemplate, UsersService usersService) {
        // 拿到用户信息之后更新用户对应的信息
        user.setExperience(user.getExperience() + task.getDay_exp());
        user.setAssets(user.getAssets() + task.getDay_point());
        // 记录次数
        String redisKey = "sign_" + user.getUid();
        String accKey = "accSign_" + user.getUid();
        boolean is_sign = Boolean.TRUE.equals(redisTemplate.hasKey(redisKey));
        int taskCount = 0;
        Object value = redisHelp.getRedis(accKey, redisTemplate);
        if (value != null) {
            taskCount = Integer.parseInt((String) value )+ 1;
        }
        int secondsUntilEndOfDay = baseFull.endTime();
        redisHelp.setRedis(redisKey, String.valueOf(taskCount), secondsUntilEndOfDay, redisTemplate);
        redisHelp.setRedis(accKey, String.valueOf(taskCount), 86400 * 365, redisTemplate);
        if (!is_sign) {
            usersService.update(user);
        }
    }

    private void mark(Users user, Task task, RedisTemplate redisTemplate, UsersService usersService) {
        // 拿到用户信息之后更新用户对应的信息
        user.setExperience(user.getExperience() + task.getMark_exp());
        user.setAssets(user.getAssets() + task.getMark_point());
        // 记录次数
        String redisKey = "mark_" + user.getUid();
        int taskCount = 0;
        Object value = redisHelp.getRedis(redisKey, redisTemplate);
        if (value != null) {
            taskCount = Integer.parseInt((String) value )+ 1;
        }
        int secondsUntilEndOfDay = baseFull.endTime();
        redisHelp.setRedis(redisKey, String.valueOf(taskCount), secondsUntilEndOfDay, redisTemplate);
        if (taskCount < task.getMark_times()) {
            usersService.update(user);
        }
    }

    private void view(Users user, Task task, RedisTemplate redisTemplate, UsersService usersService) {
        // 拿到用户信息之后更新用户对应的信息
        user.setExperience(user.getExperience() + task.getView_exp());
        user.setAssets(user.getAssets() + task.getView_point());
        // 记录次数
        String redisKey = "view_" + user.getUid();
        int taskCount = 0;
        Object value = redisHelp.getRedis(redisKey, redisTemplate);
        if (value != null) {
            taskCount = Integer.parseInt((String) value )+ 1;
        }
        int secondsUntilEndOfDay = baseFull.endTime();
        redisHelp.setRedis(redisKey, String.valueOf(taskCount), secondsUntilEndOfDay, redisTemplate);
        if (taskCount < task.getView_times()) {
            usersService.update(user);
        }
    }
}
