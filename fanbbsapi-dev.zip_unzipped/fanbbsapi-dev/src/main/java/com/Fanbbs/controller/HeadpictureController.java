package com.Fanbbs.controller;

import com.Fanbbs.common.*;
import com.alibaba.fastjson.JSONArray;
import com.alibaba.fastjson.JSONObject;
import com.Fanbbs.entity.*;
import com.Fanbbs.service.*;
import com.auth0.jwt.interfaces.DecodedJWT;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.data.redis.core.RedisTemplate;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.stereotype.Component;
import org.springframework.stereotype.Controller;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.ResponseBody;

import javax.servlet.http.HttpServletRequest;
import java.util.*;

@Component
@Controller
@RequestMapping(value = "/headpicture")
public class HeadpictureController {
    @Autowired
    private UsersService usersService;

    @Autowired
    private HeadpictureService service;

    ResultAll Result = new ResultAll();


    /***
     *
     * @param link 链接
     * @param name 名称
     * @param request
     * @return
     */

    @RequestMapping(value = "/add")
    @ResponseBody
    public String headAdd(@RequestParam(value = "link") String link,
                          @RequestParam(value = "name", required = false) String name,
                          @RequestParam(value = "permission", required = false, defaultValue = "1") Integer permission,
                          HttpServletRequest request) {

        try {
            String token = request.getHeader("Authorization");
            Users user = getUser(token);
            if (!permission(user)) return Result.getResultJson(201, "无权限", null);
            Long timeStamp = System.currentTimeMillis() / 1000;
            Headpicture headpicture = new Headpicture();
            headpicture.setStatus(1);
            headpicture.setLink(link);
            headpicture.setType(1);
            headpicture.setPermission(permission);
            headpicture.setName(name);
            headpicture.setCreator(user.getUid());
            service.insert(headpicture);

            return Result.getResultJson(200, "添加完成", null);

        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }
    }

    /***
     *
     * @param page
     * @param limit
     * @param order
     * @param request
     * @return
     */

    @RequestMapping(value = "/list")
    @ResponseBody
    public String list(@RequestParam(value = "page", required = false, defaultValue = "1") Integer page,
                       @RequestParam(value = "limit", required = false, defaultValue = "10") Integer limit,
                       @RequestParam(value = "order", required = false) String order,
                       HttpServletRequest request) {
        try {
            String token = request.getHeader("Authorization");
            Users user = getUser(token);
            Headpicture headpicture = new Headpicture();
            headpicture.setStatus(1);
            headpicture.setType(1);
            if (permission(user)) {
                headpicture.setType(null);
                headpicture.setPermission(null);
                headpicture.setStatus(null);
                headpicture.setType(null);
            }
            PageList<Headpicture> headpicturePageList = service.selectPage(headpicture, page, limit, order);
            List<Headpicture> headpictureList = headpicturePageList.getList();

            Map<String, Object> data = new HashMap<>();
            data.put("page", page);
            data.put("limit", limit);
            data.put("data", headpictureList);
            data.put("count", headpictureList.size());
            data.put("total", service.total(headpicture));

            return Result.getResultJson(200, "获取成功", data);

        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }
    }

    // 设置头像框
    @RequestMapping(value = "/set")
    @ResponseBody
    public String set(HttpServletRequest request, @RequestParam(value = "id") Integer id) {
        try {
            String token = request.getHeader("Authorization");
            Users user = getUser(token);
            if (user == null || user.getUid() == null) return Result.getResultJson(201, "用户不存在", null);
            boolean permission = permission(user);
            // 检查传入id是否存在
            Headpicture headpicture = service.selectByKey(id);
            if (headpicture == null || headpicture.toString().isEmpty())
                return Result.getResultJson(201, "头像框不存在", null);

            JSONArray head_picture = user.getHead_picture() != null ? JSONArray.parseArray(user.getHead_picture()) : new JSONArray();
            JSONObject opt = user.getOpt() != null ? JSONObject.parseObject(user.getOpt()) : null;
            if (opt == null) opt = new JSONObject();
            // 先判断头像框权限 权限为1 就需要拥有该头像 或者是管理员
            if (headpicture != null && headpicture.getPermission().equals(1) && !head_picture.contains(headpicture.getId()) && !permission) {
                return Result.getResultJson(201, "你没有拥有该头像框", null);
            }
            opt.put("head_picture", headpicture.getLink());
            //设置用户opt
            user.setOpt(opt.toString());
            usersService.update(user);

            return Result.getResultJson(200, "设置成功", null);

        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }
    }

    /***
     * 清除头像框
     * @param request
     * @return
     */

    @RequestMapping(value = "/clear")
    @ResponseBody
    public String clear(HttpServletRequest request) {
        try {
            String token = request.getHeader("Authorization");
            Users user = getUser(token);
            if (user.getUid() == null) return Result.getResultJson(201, "用户不存在", null);
            if (user.getOpt() != null && !user.getOpt().toString().isEmpty()) {
                // 将opt格式化成Object
                JSONObject opt = JSONObject.parseObject(user.getOpt());
                // 清空opt中的head_picture数据
                opt.put("head_picture", null);

                // 写入数据库
                user.setOpt(opt.toString());
                usersService.update(user);
            }

            return Result.getResultJson(200, "已取消头像框", null);
        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }
    }

    /***
     *
     * @param id
     * @param request
     * @return
     */
    @RequestMapping(value = "/delete")
    @ResponseBody
    public String delete(@RequestParam(value = "id") Integer id, HttpServletRequest request) {
        try {
            String token = request.getHeader("Authorization");
            Users user = getUser(token);
            if (!permission(user)) return Result.getResultJson(201, "无权限", null);

            // 查询头像框是否存在
            Headpicture headpicture = service.selectByKey(id);
            if (headpicture == null || headpicture.toString().isEmpty())
                return Result.getResultJson(201, "头像框不存在", null);

            service.delete(headpicture.getId());
            return Result.getResultJson(200, "删除成功", null);
        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }
    }


    @RequestMapping(value = "/update")
    @ResponseBody
    public String update(@RequestParam(value = "id") Integer id,
                         @RequestParam(value = "link") String link,
                         @RequestParam(value = "name") String name,
                         @RequestParam(value = "permission") Integer permission,
                         @RequestParam(value = "status") Integer status,
                         @RequestParam(value = "type") Integer type,
                         HttpServletRequest request) {
        try {

            Users user = getUser(request.getHeader("Authorization"));
            if (!permission(user)) return Result.getResultJson(201, "无权限", null);
            Headpicture headpicture = service.selectByKey(id);
            if (headpicture.getId() == null) return Result.getResultJson(201, "数据不存在", null);
            headpicture.setStatus(status);
            headpicture.setLink(link);
            headpicture.setName(name);
            headpicture.setPermission(permission);
            headpicture.setType(type);

            service.update(headpicture);
            return Result.getResultJson(200, "修改成功", null);

        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }
    }


    @PostMapping(value = "/give")
    @ResponseBody
    public String give(@RequestParam(value = "id") Integer id,
                       @RequestParam(value = "user_id") Integer user_id,
                       HttpServletRequest request) {
        try {
            String token = request.getHeader("Authorization");
            Users user = getUser(token);
            if (!permission(user)) return Result.getResultJson(201, "无权限", null);

            // 查询头像框是否存在
            Headpicture headpicture = service.selectByKey(id);
            if (headpicture.getId() == null) return Result.getResultJson(201, "头像框不存在", null);
            // 查询用户是否存在
            Users giveUser = usersService.selectByKey(user_id);
            if (giveUser.getUid() == null) return Result.getResultJson(201, "用户不存在", null);
            // 如果存在就格式化用户的head_picture
            JSONArray head_picture = new JSONArray();
            if (user.getHead_picture() != null) {
                head_picture = JSONArray.parseArray(user.getHead_picture());
                if (head_picture.contains(id)) return Result.getResultJson(201, "用户已拥有该头像框", null);
                head_picture.add(headpicture.getId());
            } else {
                head_picture.add(headpicture.getId());
            }
            giveUser.setHead_picture(head_picture.toString());
            return Result.getResultJson(200, "赋予成功", null);
        } catch (Exception e) {
            e.printStackTrace();
            return Result.getResultJson(400, "接口异常", null);
        }
    }


    // 检查是否是 Administrator 或者 editor
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

