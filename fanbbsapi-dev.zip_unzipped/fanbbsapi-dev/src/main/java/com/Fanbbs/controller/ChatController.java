package com.Fanbbs.controller;

import com.Fanbbs.config.websocket;
import com.Fanbbs.common.*;
import com.Fanbbs.entity.Chat;
import com.Fanbbs.entity.ChatMsg;
import com.Fanbbs.entity.Users;
import com.Fanbbs.service.*;
import com.alibaba.fastjson.JSONArray;
import com.alibaba.fastjson.JSONObject;
import com.auth0.jwt.interfaces.DecodedJWT;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.stereotype.Controller;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.ResponseBody;

import javax.servlet.http.HttpServletRequest;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

/**
 * 控制层
 * TypechoChatController
 *
 * @author buxia97
 * @date 2023/01/10
 */
@Controller
@RequestMapping(value = "/chat")
public class ChatController {

    @Autowired
    ChatService service;

    @Autowired
    ChatMsgService chatMsgService;

    @Autowired
    private ApiconfigService apiconfigService;

    @Autowired
    private HeadpictureService headpictureService;

    @Autowired
    private UsersService usersService;

    websocket websocket = new websocket();

    ResultAll Result = new ResultAll();
    private Map data;

    /***
     * 获取聊天室id
     */

    @RequestMapping(value = "/getChatId")
    @ResponseBody
    public String getChatId(@RequestParam(value = "receiver_id") Integer receiver_id,
                            HttpServletRequest request) {
        try {
            String token = request.getHeader("Authorization");
            Users user = getUser(token);
            // 查询接收用户是否存在
            Users receiveUser = usersService.selectByKey(receiver_id);
            if (receiveUser == null || receiveUser.toString().isEmpty())
                return Result.getResultJson(201, "目标用户不存在", null);

            // 验证结束 开始查询聊天列表是否存在
            Chat chat = new Chat();
            chat.setSender_id(user.getUid());
            chat.setReceiver_id(receiveUser.getUid());
            List<Chat> chatList = service.selectList(chat);
            // 不存在就插入一条数据
            Chat newChat = new Chat();
            if (chatList.isEmpty()) {
                newChat.setType(0);
                newChat.setReceiver_id(receiveUser.getUid());
                newChat.setSender_id(user.getUid());
                service.insert(newChat);
                newChat = service.selectByKey(newChat.getId());
                Map data = JSONObject.parseObject(JSONObject.toJSONString(newChat), Map.class);
                return Result.getResultJson(200, "获取成功", data);
            }
            Map data = JSONObject.parseObject(JSONObject.toJSONString(chatList.get(0)), Map.class);
            return Result.getResultJson(200, "获取成功", data);
        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }
    }

    /***
     * 用户聊天记录
     * @param id 接收者用户id
     */
    @RequestMapping(value = "/chatRecord")
    @ResponseBody
    public String chatRecord(@RequestParam(value = "id") Integer id,
                             @RequestParam(value = "page", required = false, defaultValue = "1") Integer page,
                             @RequestParam(value = "limit", required = false, defaultValue = "20") Integer limit,
                             @RequestParam(value = "order", required = false, defaultValue = "created asc") String order,
                             HttpServletRequest request) {
        try {
            String token = request.getHeader("Authorization");
            Users user = getUser(token);
            if (user == null || user.getUid() == null) return Result.getResultJson(201, "用户不存在", null);
            // 查找聊天室 是否存在
            Chat chat = service.selectByKey(id);
            if (chat == null || chat.toString().isEmpty()) return Result.getResultJson(201, "聊天室不存在", null);

            Users receiverUser = new Users();
            // 是否是查询对面
            if (chat.getReceiver_id().equals(user.getUid())) {
                receiverUser = usersService.selectByKey(chat.getSender_id());
            } else {
                receiverUser = usersService.selectByKey(chat.getReceiver_id());
            }
            Map<String, Object> data = JSONObject.parseObject(JSONObject.toJSONString(receiverUser));
            // 格式化opt
            JSONObject opt = receiverUser != null && receiverUser.getOpt() != null ? JSONObject.parseObject(receiverUser.getOpt()) : new JSONObject();
            data.put("opt", opt);
            data.remove("head_picture");
            data.remove("password");
            data.remove("mail");
            data.remove("address");

            // 查询聊天室聊天记录
            ChatMsg chatMsg = new ChatMsg();
            chatMsg.setChat_id(chat.getId());
            PageList<ChatMsg> chatMsgPageList = chatMsgService.selectPage(chatMsg, page, limit, order);
            List<ChatMsg> chatMsgList = chatMsgPageList.getList();

            JSONArray dataList = new JSONArray();
            for (ChatMsg _chatMsg : chatMsgList) {
                Map<String, Object> msgData = JSONObject.parseObject(JSONObject.toJSONString(_chatMsg), Map.class);
                msgData.put("userInfo", new HashMap<>(data));
                dataList.add(msgData);
            }
            Map<String, Object> result = new HashMap<>();
            result.put("page", page);
            result.put("limit", limit);
            result.put("data", dataList);
            result.put("count", dataList.size());
            result.put("total", chatMsgPageList.getTotalCount());

            return Result.getResultJson(200, "获取成功", result);
        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }
    }

    /***
     * 发送消息
     */
    @RequestMapping(value = "/sendMsg")
    @ResponseBody
    public String sendMsg(@RequestParam(value = "id") Integer id,
                          @RequestParam(value = "text") String text,
                          HttpServletRequest request) {
        try {
            String token = request.getHeader("Authorization");
            Users user = getUser(token);
            if (user == null || user.getUid() == null)
                return Result.getResultJson(201, "用户不存在，请重新登录", null);
            if (user.getBantime() != null && !user.getBantime().toString().isEmpty() && user.getBantime() > System.currentTimeMillis() / 100)
                return Result.getResultJson(201, "用户封禁中", null);

            if (text == null || text.isEmpty())
                return Result.getResultJson(201, "请输入消息", null);

            // 查询聊天室是否存在
            Chat chat = service.selectByKey(id);
            if (chat == null || chat.toString().isEmpty()) return Result.getResultJson(202, "聊天室不存在", null);
            // 写入信息
            ChatMsg chatMsg = new ChatMsg();
            chatMsg.setType(chat.getType());
            chatMsg.setSender_id(user.getUid());
            chatMsg.setChat_id(chat.getId());
            chatMsg.setText(text);
            chatMsg.setCreated((int) (System.currentTimeMillis() / 1000));
            chatMsgService.insert(chatMsg);

            chat.setLastTime((int) (System.currentTimeMillis() / 1000));
            service.update(chat);
            // 将信息返回
            Map<String, Object> data = JSONObject.parseObject(JSONObject.toJSONString(chatMsg), Map.class);
            // 使用webSocket 给目标用户发消息
            try {
                int receivcer_id = chat.getReceiver_id().equals(user.getUid()) ? chat.getSender_id() : chat.getReceiver_id();
                int sender_id = chat.getSender_id().equals(user.getUid()) ? chat.getSender_id() : chat.getReceiver_id();
                JSONObject stringJson = new JSONObject();
                stringJson.put("sender_id", sender_id);
                stringJson.put("type", "message");
                stringJson.put("text", text);
                stringJson.put("chat_id", chat.getId());
                websocket.sendChatText(stringJson.toJSONString(), receivcer_id);
            } catch (Exception e) {
                e.printStackTrace();
            }

            return Result.getResultJson(200, "发送成功", data);

        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }

    }

    /***
     * 获取聊天列表
     */

    @RequestMapping("/chatList")
    @ResponseBody
    public String chatList(@RequestParam(value = "page", required = false, defaultValue = "1") Integer page,
                           @RequestParam(value = "limit", required = false, defaultValue = "20") Integer limit,
                           @RequestParam(value = "order", required = false, defaultValue = "lastTime desc") String order,
                           HttpServletRequest request) {
        try {
            String token = request.getHeader("Authorization");
            Users user = getUser(token);
            if (user.getUid() == null) return Result.getResultJson(201, "用户不存在，请重新登录", null);
            // 查询聊天列表
            Chat query = new Chat();
            query.setSender_id(user.getUid());
            PageList<Chat> chatPageList = service.selectPage(query, page, limit, order, null);
            List<Chat> chatList = chatPageList.getList();
            List dataList = new ArrayList<>();
            for (Chat _chat : chatList) {
                Map data = JSONObject.parseObject(JSONObject.toJSONString(_chat), Map.class);
                // 只需要查询到对面的信息就行
                Users userInfo = new Users();
                if (!_chat.getSender_id().equals(user.getUid())) {
                    userInfo = usersService.selectByKey(_chat.getSender_id());
                } else {
                    userInfo = usersService.selectByKey(_chat.getReceiver_id());
                }
                if (userInfo == null || userInfo.getUid() == null) continue;
                // 格式化成map
                Map userData = JSONObject.parseObject(JSONObject.toJSONString(userInfo), Map.class) != null ? JSONObject.parseObject(JSONObject.toJSONString(userInfo), Map.class) : new HashMap<>();
                if (userInfo == null || userInfo.getUid() != null) {
                    userData.remove("password");
                    userData.remove("email");
                    userData.remove("head_picture");
                    userData.remove("rank");
                    userData.remove("opt");
                } else {
                    userData.put("screenName", "用户已注销");
                    userData.put("uid", 0);
                }
                data.put("userInfo", userData);
                dataList.add(data);
            }
            Map data = new HashMap<>();
            data.put("count", chatList.size());
            data.put("total", chatPageList.getTotalCount());
            data.put("data", dataList);
            return Result.getResultJson(200, "获取成功", data);

        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }
    }

    /***
     * 权限判断
     *
     * @param user
     * @return
     */
    private boolean permission(Users user) {
        if (user.getUid() == null || user.getUid().equals(0))
            return false;
        if (user.getGroup().equals("administrator") || user.getGroup().equals("editor"))
            return true;
        return false;
    }

    /***
     * 获取用户信息
     *
     * @param token
     * @return
     */
    private Users getUser(String token) {
        if (token == null || token.isEmpty())
            return new Users();
        // 获取用户信息
        DecodedJWT verify = JWT.verify(token);
        Users user = usersService.selectByKey(Integer.parseInt(verify.getClaim("aud").asString()));
        return user;
    }

}
